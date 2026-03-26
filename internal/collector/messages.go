package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
)

// UserMessage represents a single user message extracted from a session log.
type UserMessage struct {
	Timestamp time.Time
	Content   string
}

// ReadUserMessages reads user messages from a session log file.
// Returns at most maxMessages, newest last.
func ReadUserMessages(platform event.Platform, sessionID, cwd string, maxMessages int) []UserMessage {
	switch platform {
	case event.PlatformCodex:
		return readCodexUserMessages(sessionID, maxMessages)
	case event.PlatformClaude:
		return readClaudeUserMessages(sessionID, cwd, maxMessages)
	default:
		if messages := readClaudeUserMessages(sessionID, cwd, maxMessages); len(messages) > 0 {
			return messages
		}
		return readCodexUserMessages(sessionID, maxMessages)
	}
}

func readClaudeUserMessages(sessionID, cwd string, maxMessages int) []UserMessage {
	logPath := findClaudeLogPath(sessionID, cwd)
	if logPath == "" {
		return nil
	}
	return readClaudeMessagesFromFile(logPath, maxMessages)
}

func findClaudeLogPath(sessionID, cwd string) string {
	home, _ := os.UserHomeDir()
	if home == "" || sessionID == "" {
		return ""
	}

	baseDir := filepath.Join(home, ".claude", "projects")
	if cwd != "" {
		cwdEncoded := strings.ReplaceAll(cwd, "/", "-")
		logPath := filepath.Join(baseDir, cwdEncoded, sessionID+".jsonl")
		if _, err := os.Stat(logPath); err == nil {
			return logPath
		}
	}

	matches, err := filepath.Glob(filepath.Join(baseDir, "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func readClaudeMessagesFromFile(logPath string, maxMessages int) []UserMessage {
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var messages []UserMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 2*1024*1024) // handle large lines

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Quick pre-check to avoid full JSON parse on every line
		if !bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"type": "user"`)) {
			continue
		}

		var entry struct {
			Type      string `json:"type"`
			IsMeta    bool   `json:"isMeta"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Type != "user" || entry.IsMeta {
			continue
		}

		content := extractClaudeMessageText(entry.Message.Content)
		if shouldSkipMessageContent(content) {
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)

		messages = append(messages, UserMessage{
			Timestamp: ts.Local(),
			Content:   content,
		})
	}

	// Return last N messages
	if maxMessages > 0 && len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
	return messages
}

func extractClaudeMessageText(raw json.RawMessage) string {
	var content string
	if json.Unmarshal(raw, &content) == nil {
		return strings.TrimSpace(content)
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}

	var parts []string
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		text := strings.TrimSpace(block.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func readCodexUserMessages(sessionID string, maxMessages int) []UserMessage {
	logPath := findCodexLogPath(sessionID)
	if logPath == "" {
		return nil
	}

	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var messages []UserMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 ||
			!bytes.Contains(line, []byte(`"type":"response_item"`)) ||
			!bytes.Contains(line, []byte(`"role":"user"`)) {
			continue
		}

		var entry codexLogEntry
		if json.Unmarshal(line, &entry) != nil || entry.Type != "response_item" {
			continue
		}

		var payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(entry.Payload, &payload) != nil {
			continue
		}
		if payload.Type != "message" || payload.Role != "user" {
			continue
		}

		content := extractCodexMessageText(payload.Content)
		if shouldSkipMessageContent(content) {
			continue
		}

		messages = append(messages, UserMessage{
			Timestamp: parseTimestamp(entry.Timestamp).Local(),
			Content:   content,
		})
	}

	if maxMessages > 0 && len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
	return messages
}

func findCodexLogPath(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	// Fast path: use the watcher's in-memory index if available.
	if codexPathResolver != nil {
		if p := codexPathResolver(sessionID); p != "" {
			return p
		}
	}
	// Slow path: recursive walk (Codex may store files in dated subdirs).
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	baseDir := filepath.Join(home, ".codex", "sessions")
	suffix := sessionID + ".jsonl"
	var match string
	_ = filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), suffix) {
			match = path
			return filepath.SkipAll
		}
		return nil
	})
	return match
}

func extractCodexMessageText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}

	var parts []string
	for _, block := range blocks {
		if block.Type != "input_text" && block.Type != "text" {
			continue
		}
		text := strings.TrimSpace(block.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func shouldSkipMessageContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	if strings.HasPrefix(content, "<") {
		return true
	}
	if strings.HasPrefix(content, "# AGENTS.md instructions") {
		return true
	}
	return false
}
