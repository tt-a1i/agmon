package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

// ClaudeLogWatcher scans ~/.claude/projects/*/*.jsonl for token usage.
// Only files modified within the last 30 days are processed.
const claudeLogMaxAge = 30 * 24 * time.Hour

type ClaudeLogWatcher struct {
	baseDir          string
	emitFn           func(event.Event)
	done             chan struct{}
	stopOnce         sync.Once
	loopWG           sync.WaitGroup
	seen             map[string]int64  // file path -> last committed byte offset
	sessionGitBranch map[string]string // session_id -> git_branch
	initialScanDone  bool
	tickInterval     time.Duration
	scanFn           func()
}

func NewClaudeLogWatcher(emitFn func(event.Event)) *ClaudeLogWatcher {
	home, _ := os.UserHomeDir()
	return &ClaudeLogWatcher{
		baseDir:          filepath.Join(home, ".claude", "projects"),
		emitFn:           emitFn,
		done:             make(chan struct{}),
		seen:             make(map[string]int64),
		sessionGitBranch: make(map[string]string),
		tickInterval:     3 * time.Second,
	}
}

func (w *ClaudeLogWatcher) Start() {
	w.loopWG.Add(1)
	go func() {
		defer w.loopWG.Done()
		w.pollLoop()
	}()
}

func (w *ClaudeLogWatcher) Stop() {
	w.stopOnce.Do(func() { close(w.done) })
	w.loopWG.Wait()
}

func (w *ClaudeLogWatcher) pollLoop() {
	interval := w.tickInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			if w.scanFn != nil {
				w.scanFn()
				continue
			}
			w.scanLogs()
		}
	}
}

type claudeFileJob struct {
	path      string
	sessionID string
	size      int64
}

type claudeFileResult struct {
	path      string
	offset    int64
	sessionID string
	gitBranch string
	events    []event.Event
}

func (w *ClaudeLogWatcher) scanLogs() {
	projectDirs, err := os.ReadDir(w.baseDir)
	if err != nil {
		log.Printf("claude watcher read base dir %s: %v", w.baseDir, err)
		return
	}

	var jobs []claudeFileJob
	for _, projectDir := range projectDirs {
		if !projectDir.IsDir() {
			continue
		}
		projectPath := filepath.Join(w.baseDir, projectDir.Name())
		files, err := os.ReadDir(projectPath)
		if err != nil {
			log.Printf("claude watcher read project dir %s: %v", projectPath, err)
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > claudeLogMaxAge {
				continue
			}
			path := filepath.Join(projectPath, f.Name())
			if offset, seen := w.seen[path]; seen && info.Size() <= offset {
				continue
			}
			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			jobs = append(jobs, claudeFileJob{path: path, sessionID: sessionID, size: info.Size()})
		}
	}

	if len(jobs) == 0 {
		w.initialScanDone = true
		return
	}

	// First scan: process files in parallel.
	if !w.initialScanDone {
		w.scanParallel(jobs)
		w.initialScanDone = true
		return
	}

	// Incremental: process serially (few files, low overhead).
	for _, j := range jobs {
		w.processFile(j.path, j.sessionID)
	}
}

func (w *ClaudeLogWatcher) scanParallel(jobs []claudeFileJob) {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	jobCh := make(chan claudeFileJob, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	resultCh := make(chan claudeFileResult, len(jobs))
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for job := range jobCh {
				r := processClaudeFileCollect(
					job.path, job.sessionID, w.seen[job.path],
					w.sessionGitBranch[job.sessionID],
				)
				resultCh <- r
			}
		}()
	}
	wg.Wait()
	close(resultCh)

	for r := range resultCh {
		w.seen[r.path] = r.offset
		if r.gitBranch != "" {
			w.sessionGitBranch[r.sessionID] = r.gitBranch
		}
		for _, ev := range r.events {
			w.emitFn(ev)
		}
	}
}

// processClaudeFileCollect parses a Claude JSONL file without touching watcher state.
func processClaudeFileCollect(path, sessionID string, startOffset int64, prevGitBranch string) claudeFileResult {
	result := claudeFileResult{path: path, offset: startOffset, sessionID: sessionID, gitBranch: prevGitBranch}

	info, err := os.Stat(path)
	if err != nil {
		return result
	}
	if info.Size() <= startOffset {
		return result
	}

	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()

	if startOffset > 0 {
		if _, err := f.Seek(startOffset, 0); err != nil {
			return result
		}
	}

	reader := bufio.NewReaderSize(f, 1024*1024)
	committedOffset := startOffset

	for {
		lineBytes, err := reader.ReadBytes('\n')

		if len(lineBytes) > 0 {
			line := bytes.TrimRight(lineBytes, "\r\n")

			if len(line) > 0 {
				var entry claudeLogEntry
				if json.Unmarshal(line, &entry) == nil {
					if result.gitBranch == "" && entry.GitBranch != "" {
						result.gitBranch = entry.GitBranch
					}

					if entry.Type == "assistant" && entry.Message != nil && entry.Message.Usage != nil {
						usage := entry.Message.Usage
						model := entry.Message.Model
						totalInput := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
						cost := EstimateClaudeCost(usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, model)

						evTime := parseTimestamp(entry.Timestamp)
						if evTime.IsZero() {
							evTime = time.Now()
						}

						result.events = append(result.events, event.Event{
							ID:        fmt.Sprintf("claude-tokens-%s-%s", sessionID, entry.UUID),
							Type:      event.EventTokenUsage,
							SessionID: sessionID,
							Platform:  event.PlatformClaude,
							Timestamp: evTime,
							Data: event.EventData{
								InputTokens:         totalInput,
								OutputTokens:        usage.OutputTokens,
								CacheCreationTokens: usage.CacheCreationInputTokens,
								CacheReadTokens:     usage.CacheReadInputTokens,
								Model:               model,
								CostUSD:             cost,
								GitBranch:           result.gitBranch,
								CWD:                 entry.CWD,
							},
						})
					}
				}
			}

			if err == nil {
				committedOffset += int64(len(lineBytes))
			}
		}

		if err != nil {
			break
		}
	}

	result.offset = committedOffset
	return result
}

type claudeLogEntry struct {
	Type      string        `json:"type"`
	SessionID string        `json:"sessionId"`
	UUID      string        `json:"uuid"`
	GitBranch string        `json:"gitBranch"`
	CWD       string        `json:"cwd"`
	Timestamp string        `json:"timestamp"`
	Message   *claudeLogMsg `json:"message,omitempty"`
}

type claudeLogMsg struct {
	Model string          `json:"model"`
	Usage *claudeLogUsage `json:"usage,omitempty"`
}

type claudeLogUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (w *ClaudeLogWatcher) processFile(path, sessionID string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}

	offset := w.seen[path]
	if info.Size() <= offset {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, 0); err != nil {
			return
		}
	}

	_, hasGitBranch := w.sessionGitBranch[sessionID]

	reader := bufio.NewReaderSize(f, 1024*1024)
	committedOffset := offset

	for {
		lineBytes, err := reader.ReadBytes('\n')

		if len(lineBytes) > 0 {
			line := bytes.TrimRight(lineBytes, "\r\n")

			if len(line) > 0 {
				var entry claudeLogEntry
				if json.Unmarshal(line, &entry) == nil {
					// Collect git_branch from any line that has it.
					if !hasGitBranch && entry.GitBranch != "" {
						w.sessionGitBranch[sessionID] = entry.GitBranch
						hasGitBranch = true
					}

					// Only assistant messages carry token usage.
					if entry.Type == "assistant" && entry.Message != nil && entry.Message.Usage != nil {
						usage := entry.Message.Usage
						model := entry.Message.Model
						totalInput := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
						cost := EstimateClaudeCost(usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, model)

						// Use the actual timestamp from the JSONL entry so historical
						// sessions get their real start_time, not time.Now().
						evTime := parseTimestamp(entry.Timestamp)
						if evTime.IsZero() {
							evTime = time.Now()
						}

						w.emitFn(event.Event{
							ID:        fmt.Sprintf("claude-tokens-%s-%s", sessionID, entry.UUID),
							Type:      event.EventTokenUsage,
							SessionID: sessionID,
							Platform:  event.PlatformClaude,
							Timestamp: evTime,
							Data: event.EventData{
								// InputTokens = total (input + cacheCreate + cacheRead) for context tracking.
								InputTokens:         totalInput,
								OutputTokens:        usage.OutputTokens,
								CacheCreationTokens: usage.CacheCreationInputTokens,
								CacheReadTokens:     usage.CacheReadInputTokens,
								Model:               model,
								CostUSD:             cost,
								// Carry git_branch so the daemon can set it on the session.
								GitBranch: w.sessionGitBranch[sessionID],
								CWD:       entry.CWD,
							},
						})
					}
				}
			}

			if err == nil {
				committedOffset += int64(len(lineBytes))
			}
		}

		if err != nil {
			break
		}
	}

	w.seen[path] = committedOffset
}
