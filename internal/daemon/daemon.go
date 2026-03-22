package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/tt-a1i/agmon/internal/event"
	"github.com/tt-a1i/agmon/internal/storage"
)

type Daemon struct {
	db          *storage.DB
	sockPath    string
	listener    net.Listener
	subListener net.Listener
	mu          sync.RWMutex
	subs        []chan event.Event
	remoteSubs  map[net.Conn]struct{}
	done        chan struct{}
	stopOnce    sync.Once
}

func New(db *storage.DB, sockPath string) *Daemon {
	return &Daemon{
		db:         db,
		sockPath:   sockPath,
		remoteSubs: make(map[net.Conn]struct{}),
		done:       make(chan struct{}),
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
	localSubs := append([]chan event.Event(nil), d.subs...)
	remoteSubs := make([]net.Conn, 0, len(d.remoteSubs))
	for conn := range d.remoteSubs {
		remoteSubs = append(remoteSubs, conn)
	}
	d.mu.RUnlock()

	for _, ch := range localSubs {
		select {
		case ch <- ev:
		default:
			// drop if subscriber is slow
		}
	}
	for _, conn := range remoteSubs {
		if err := writeRemoteEvent(conn, ev); err != nil {
			d.removeRemoteSub(conn)
		}
	}
}

func (d *Daemon) Start() error {
	ln, err := listenSocket(d.sockPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = ln

	subLn, err := listenSocket(subscriberSocketPath(d.sockPath))
	if err != nil {
		ln.Close()
		cleanupSocket(d.sockPath)
		return fmt.Errorf("listen subscriber: %w", err)
	}
	d.subListener = subLn
	log.Printf("daemon listening on %s", d.sockPath)

	// Clean up sessions that never received a SessionEnd (e.g. process killed).
	if err := d.db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
		log.Printf("stale session cleanup: %v", err)
	}

	go d.acceptLoop()
	go d.acceptSubscriberLoop()
	return nil
}

func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		close(d.done)
		if d.listener != nil {
			d.listener.Close()
		}
		if d.subListener != nil {
			d.subListener.Close()
		}
		d.mu.Lock()
		for conn := range d.remoteSubs {
			conn.Close()
			delete(d.remoteSubs, conn)
		}
		d.mu.Unlock()
		cleanupSocket(d.sockPath)
		cleanupSocket(subscriberSocketPath(d.sockPath))
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

func (d *Daemon) acceptSubscriberLoop() {
	for {
		conn, err := d.subListener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return
			default:
				log.Printf("subscriber accept error: %v", err)
				continue
			}
		}
		d.addRemoteSub(conn)
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
	// Auto-create session for any event with a session ID.
	// For historical events (>2h old, e.g. from log watcher), mark session as ended
	// immediately so they don't appear as "running".
	if ev.SessionID != "" && ev.Type != event.EventSessionEnd {
		d.db.UpsertSession(ev.SessionID, ev.Platform, ev.Timestamp)
		if time.Since(ev.Timestamp) > 2*time.Hour && ev.Type == event.EventTokenUsage {
			d.db.EndSession(ev.SessionID, ev.Timestamp)
		}
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

func (d *Daemon) addRemoteSub(conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.remoteSubs[conn] = struct{}{}
}

func (d *Daemon) removeRemoteSub(conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.remoteSubs[conn]; ok {
		delete(d.remoteSubs, conn)
		conn.Close()
	}
}

func writeRemoteEvent(conn net.Conn, ev event.Event) error {
	if err := conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		return err
	}
	defer conn.SetWriteDeadline(time.Time{})
	return json.NewEncoder(conn).Encode(ev)
}
