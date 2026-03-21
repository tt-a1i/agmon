package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

type Daemon struct {
	db       *storage.DB
	sockPath string
	listener net.Listener
	mu       sync.RWMutex
	subs     []chan event.Event
	done     chan struct{}
	stopOnce sync.Once
}

func DefaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agmon", "agmon.sock")
}

func New(db *storage.DB, sockPath string) *Daemon {
	return &Daemon{
		db:       db,
		sockPath: sockPath,
		done:     make(chan struct{}),
	}
}

func (d *Daemon) Subscribe() chan event.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan event.Event, 256)
	d.subs = append(d.subs, ch)
	return ch
}

func (d *Daemon) Unsubscribe(ch chan event.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, s := range d.subs {
		if s == ch {
			d.subs = append(d.subs[:i], d.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

func (d *Daemon) broadcast(ev event.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, ch := range d.subs {
		select {
		case ch <- ev:
		default:
			// drop if subscriber is slow
		}
	}
}

func (d *Daemon) Start() error {
	os.Remove(d.sockPath)
	if err := os.MkdirAll(filepath.Dir(d.sockPath), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	ln, err := net.Listen("unix", d.sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = ln
	log.Printf("daemon listening on %s", d.sockPath)

	// Clean up sessions that never received a SessionEnd (e.g. process killed).
	if err := d.db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
		log.Printf("stale session cleanup: %v", err)
	}

	go d.acceptLoop()
	return nil
}

func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		close(d.done)
		if d.listener != nil {
			d.listener.Close()
		}
		os.Remove(d.sockPath)
	})
}

func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	for {
		var ev event.Event
		if err := dec.Decode(&ev); err != nil {
			return
		}
		if err := d.processEvent(ev); err != nil {
			log.Printf("process event error: %v", err)
		}
		d.broadcast(ev)
	}
}

func (d *Daemon) processEvent(ev event.Event) error {
	// Auto-create session for any event with a session ID
	if ev.SessionID != "" && ev.Type != event.EventSessionEnd {
		d.db.UpsertSession(ev.SessionID, ev.Platform, ev.Timestamp)
	}

	switch ev.Type {
	case event.EventSessionStart:
		if ev.Data.CWD != "" || ev.Data.GitBranch != "" {
			d.db.UpdateSessionMeta(ev.SessionID, ev.Data.CWD, ev.Data.GitBranch)
		}
		return nil

	case event.EventSessionEnd:
		d.db.MarkPendingToolCallsInterrupted(ev.SessionID)
		return d.db.EndSession(ev.SessionID, ev.Timestamp)

	case event.EventAgentStart:
		return d.db.UpsertAgent(ev.AgentID, ev.SessionID, ev.Data.ParentAgentID, ev.Data.AgentRole, ev.Timestamp)

	case event.EventAgentEnd:
		return d.db.EndAgent(ev.AgentID, ev.Timestamp)

	case event.EventToolCallStart:
		return d.db.InsertToolCallStart(ev.ID, ev.AgentID, ev.SessionID, ev.Data.ToolName, ev.Data.ToolParams, ev.Timestamp)

	case event.EventToolCallEnd:
		if err := d.db.UpdateToolCallEnd(ev.ID, ev.Data.ToolResult, ev.Data.ToolStatus, ev.Data.DurationMs, ev.Timestamp); err != nil {
			return err
		}
		if ev.Data.FilePath != "" {
			return d.db.InsertFileChange(ev.SessionID, ev.Data.FilePath, ev.Data.ChangeType, ev.Timestamp)
		}
		return nil

	case event.EventTokenUsage:
		if ev.Data.GitBranch != "" || ev.Data.CWD != "" {
			d.db.UpdateSessionMeta(ev.SessionID, ev.Data.CWD, ev.Data.GitBranch)
		}
		if err := d.db.InsertTokenUsage(ev.AgentID, ev.SessionID, ev.Data.InputTokens, ev.Data.OutputTokens, ev.Data.CacheCreationTokens, ev.Data.CacheReadTokens, ev.Data.Model, ev.Data.CostUSD, ev.Timestamp, ev.ID); err != nil {
			return err
		}
		return d.db.UpdateSessionTokens(ev.SessionID)

	case event.EventFileChange:
		return d.db.InsertFileChange(ev.SessionID, ev.Data.FilePath, ev.Data.ChangeType, ev.Timestamp)
	}
	return nil
}

// ProcessExternalEvent processes an event from an external source (e.g., Codex watcher).
func (d *Daemon) ProcessExternalEvent(ev event.Event) {
	if err := d.processEvent(ev); err != nil {
		log.Printf("process external event error: %v", err)
	}
	d.broadcast(ev)
}

