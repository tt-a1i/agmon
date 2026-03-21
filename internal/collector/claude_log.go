package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	seen             map[string]int64  // file path -> last committed byte offset
	sessionGitBranch map[string]string // session_id -> git_branch
}

func NewClaudeLogWatcher(emitFn func(event.Event)) *ClaudeLogWatcher {
	home, _ := os.UserHomeDir()
	return &ClaudeLogWatcher{
		baseDir:          filepath.Join(home, ".claude", "projects"),
		emitFn:           emitFn,
		done:             make(chan struct{}),
		seen:             make(map[string]int64),
		sessionGitBranch: make(map[string]string),
	}
}

func (w *ClaudeLogWatcher) Start() {
	go w.pollLoop()
}

func (w *ClaudeLogWatcher) Stop() {
	w.stopOnce.Do(func() { close(w.done) })
}

func (w *ClaudeLogWatcher) pollLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.scanLogs()
		}
	}
}

func (w *ClaudeLogWatcher) scanLogs() {
	projectDirs, err := os.ReadDir(w.baseDir)
	if err != nil {
		return
	}
	for _, projectDir := range projectDirs {
		if !projectDir.IsDir() {
			continue
		}
		projectPath := filepath.Join(w.baseDir, projectDir.Name())
		files, err := os.ReadDir(projectPath)
		if err != nil {
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
			// Skip files not modified in the last 30 days.
			if time.Since(info.ModTime()) > claudeLogMaxAge {
				continue
			}
			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			w.processFile(filepath.Join(projectPath, f.Name()), sessionID)
		}
	}
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
						totalInput := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens

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
								InputTokens:  totalInput,
								OutputTokens: usage.OutputTokens,
								Model:        entry.Message.Model,
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
