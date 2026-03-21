package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UserMessage represents a single user message extracted from a Claude JSONL log.
type UserMessage struct {
	Timestamp time.Time
	Content   string
}

// ReadUserMessages reads user messages from a Claude session's JSONL log file.
// It looks up the file at ~/.claude/projects/<cwdEncoded>/<sessionID>.jsonl.
// Returns at most maxMessages, newest last.
func ReadUserMessages(sessionID, cwd string, maxMessages int) []UserMessage {
	if sessionID == "" || cwd == "" {
		return nil
	}

	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}

	// CWD encoding: /Users/admin/code/agmon → -Users-admin-code-agmon
	cwdEncoded := strings.ReplaceAll(cwd, "/", "-")
	logPath := filepath.Join(home, ".claude", "projects", cwdEncoded, sessionID+".jsonl")

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

		// Content can be a string or an array; we only want plain strings
		var content string
		if json.Unmarshal(entry.Message.Content, &content) != nil {
			continue
		}
		content = strings.TrimSpace(content)
		if content == "" || strings.HasPrefix(content, "<") {
			// Skip system/command messages like <command-name>, <local-command-stdout>, etc.
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)

		messages = append(messages, UserMessage{
			Timestamp: ts.Local(),
			Content:   content,
		})
	}

	// Return last N messages
	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
	return messages
}
