package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/collector"
	"github.com/tt-a1i/tokenmeter/internal/event"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

type Daemon struct {
	db          *storage.DB
	sockPath    string
	socketLock  *socketLock
	listener    net.Listener
	subListener net.Listener
	mu          sync.RWMutex
	subs        []chan event.Event
	remoteSubs  map[net.Conn]struct{}
	clientConns map[net.Conn]struct{}
	connWG      sync.WaitGroup
	stopping    bool
	done        chan struct{}
	stopOnce    sync.Once
	batchCh     chan event.Event // buffered channel for async event processing
	batchDone   chan struct{}    // closed when batch consumer exits
	bgWG        sync.WaitGroup   // background goroutines started in Start() (sweepers, etc.)

	// Observability counters. Atomic so callers/inspectors don't need the
	// daemon mutex. Currently logged at Stop(); a future /metrics endpoint
	// or web stats page can surface them live.
	droppedBroadcasts   atomic.Int64 // local subscriber channel full
	droppedShutdownEvts atomic.Int64 // ProcessExternalEventAsync after done close
	duplicateToolStarts atomic.Int64 // EventToolCallStart hit existing call_id (Pre re-emit)
}

func New(db *storage.DB, sockPath string) *Daemon {
	return &Daemon{
		db:          db,
		sockPath:    sockPath,
		remoteSubs:  make(map[net.Conn]struct{}),
		clientConns: make(map[net.Conn]struct{}),
		done:        make(chan struct{}),
		batchCh:     make(chan event.Event, 10000),
		batchDone:   make(chan struct{}),
	}
}

// Subscribe returns a buffered channel that receives every event broadcast
// by the daemon. The daemon owns sends; the caller MUST NOT close the
// returned channel — closing it races with the broadcast path, which
// snapshots `subs` under RLock and sends to each subscriber outside the
// lock, so a send-on-closed-channel panic is possible.
//
// Lifecycle contract:
//  1. Subscribe → caller spawns a reader goroutine
//  2. (Optionally) signal that reader to exit (e.g. via its own done channel)
//  3. Unsubscribe(ch) to detach from broadcast
//  4. Drop the channel reference; GC handles cleanup
//
// Buffer is 256 — events that exceed buffer capacity are dropped by
// `broadcast` rather than blocking. Slow subscribers won't stall the daemon.
func (d *Daemon) Subscribe() chan event.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan event.Event, 256)
	d.subs = append(d.subs, ch)
	return ch
}

// Unsubscribe removes ch from the broadcast set. See Subscribe for the
// caller-must-not-close contract — Unsubscribe deliberately does not close
// ch either.
func (d *Daemon) Unsubscribe(ch chan event.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, s := range d.subs {
		if s == ch {
			d.subs = append(d.subs[:i], d.subs[i+1:]...)
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
			// Subscriber buffer (256) is full — drop rather than stall the
			// daemon. Tracked so we can surface "your UI is too slow" later.
			d.droppedBroadcasts.Add(1)
		}
	}
	for _, conn := range remoteSubs {
		if err := writeRemoteEvent(conn, ev); err != nil {
			d.removeRemoteSub(conn)
		}
	}
}

func (d *Daemon) Start() error {
	lock, err := acquireSocketLock(d.sockPath)
	if err != nil {
		return fmt.Errorf("socket lock: %w", err)
	}

	ln, err := listenSocket(d.sockPath)
	if err != nil {
		lock.Close()
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = ln
	d.socketLock = lock

	subLn, err := listenSocket(subscriberSocketPath(d.sockPath))
	if err != nil {
		ln.Close()
		cleanupSocket(d.sockPath)
		lock.Close()
		return fmt.Errorf("listen subscriber: %w", err)
	}
	d.subListener = subLn
	log.Printf("daemon listening on %s", d.sockPath)

	// Clean up sessions that never received a SessionEnd (e.g. process killed).
	if err := d.db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
		log.Printf("stale session cleanup: %v", err)
	}

	// Remove phantom Codex sessions (zero tokens, zero tool calls).
	if n, err := d.db.PruneEmptyCodexSessions(); err != nil {
		log.Printf("prune empty codex sessions: %v", err)
	} else if n > 0 {
		log.Printf("pruned %d empty Codex sessions", n)
	}

	// Fix sessions with "<synthetic>" model from Claude internal messages.
	if n, err := d.db.RepairSyntheticModels(); err != nil {
		log.Printf("repair synthetic models: %v", err)
	} else if n > 0 {
		log.Printf("repaired %d sessions with synthetic model", n)
	}

	// Repair token_usage rows that have empty model (Codex turn_context ordering issue).
	d.repairEmptyTokenModels()

	go d.acceptLoop()
	go d.acceptSubscriberLoop()
	go d.batchConsumer()
	d.bgWG.Add(1)
	go func() {
		defer d.bgWG.Done()
		d.staleSweepLoop()
	}()
	return nil
}

// staleSweepLoop periodically re-runs MarkStaleSessionsEnded so a daemon that
// stays up for days doesn't leave sessions stuck in "active" once their last
// event is older than the stale threshold. The startup pass at Start() only
// catches sessions stale at boot.
func (d *Daemon) staleSweepLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if err := d.db.MarkStaleSessionsEnded(2 * time.Hour); err != nil {
				log.Printf("staleSweepLoop: %v", err)
			}
		}
	}
}

func (d *Daemon) repairEmptyTokenModels() {
	sessions, err := d.db.ListEmptyModelSessions()
	if err != nil {
		log.Printf("repair empty models: list: %v", err)
		return
	}
	var total int64
	for _, s := range sessions {
		if s.Model == "" || s.Platform != string(event.PlatformCodex) {
			continue
		}
		inP, outP, cacheP := collector.CodexPricing(s.Model)
		n, err := d.db.BackfillEmptyTokenModel(s.SessionID, s.Model, inP, outP, cacheP)
		if err != nil {
			log.Printf("repairEmptyTokenModels: backfill %s: %v", s.SessionID, err)
			continue
		}
		if n > 0 {
			if err := d.db.UpdateSessionTokens(s.SessionID); err != nil {
				log.Printf("repairEmptyTokenModels: update session %s: %v", s.SessionID, err)
			}
			total += n
		}
	}
	if total > 0 {
		log.Printf("repaired %d token rows with missing model", total)
	}
}

func (d *Daemon) reconcileCodexTokenModel(sessionID, model string, contextTime time.Time, includeRecent bool) error {
	if model == "" {
		return nil
	}
	inP, outP, cacheP := collector.CodexPricing(model)
	var total int64
	n, err := d.db.BackfillEmptyTokenModel(sessionID, model, inP, outP, cacheP)
	if err != nil {
		return err
	}
	total += n
	if includeRecent && shouldRepairRecentCodexTokenModel(contextTime) {
		n, err = d.db.BackfillRecentCodexTokenModel(sessionID, model, contextTime, 5*time.Second, inP, outP, cacheP)
		if err != nil {
			return err
		}
		total += n
	}
	if total > 0 {
		log.Printf("backfilled %d token rows for session %s with model %s", total, sessionID, model)
		return d.db.UpdateSessionTokens(sessionID)
	}
	return nil
}

func shouldRepairRecentCodexTokenModel(contextTime time.Time) bool {
	if contextTime.IsZero() {
		return false
	}
	return time.Since(contextTime) <= 2*time.Hour
}

func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.stopping = true
		d.mu.Unlock()

		close(d.done)
		if d.listener != nil {
			d.listener.Close()
		}
		if d.subListener != nil {
			d.subListener.Close()
		}
		d.mu.Lock()
		for conn := range d.clientConns {
			conn.Close()
			delete(d.clientConns, conn)
		}
		for conn := range d.remoteSubs {
			conn.Close()
			delete(d.remoteSubs, conn)
		}
		d.mu.Unlock()
		d.connWG.Wait()
		<-d.batchDone // wait for batch consumer to drain
		d.bgWG.Wait() // wait for background loops (sweepers) so they don't outlive db

		// Surface observability counters once at shutdown so operators see if
		// the daemon was leaking events to slow subscribers, dropping work
		// during the final batch flush, or hitting duplicate Pre-emits.
		if bcast, shut, dup := d.Stats(); bcast > 0 || shut > 0 || dup > 0 {
			log.Printf("daemon stats: dropped_broadcasts=%d dropped_shutdown=%d duplicate_tool_starts=%d",
				bcast, shut, dup)
		}

		cleanupSocket(d.sockPath)
		cleanupSocket(subscriberSocketPath(d.sockPath))
		if d.socketLock != nil {
			d.socketLock.Close()
			d.socketLock = nil
		}
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
		if !d.registerClientConn(conn) {
			continue
		}
		go func() {
			defer d.connWG.Done()
			d.handleConn(conn)
		}()
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
	defer d.removeClientConn(conn)
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

func (d *Daemon) registerClientConn(conn net.Conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopping {
		_ = conn.Close()
		return false
	}
	d.clientConns[conn] = struct{}{}
	d.connWG.Add(1)
	return true
}

func (d *Daemon) removeClientConn(conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.clientConns[conn]; ok {
		delete(d.clientConns, conn)
		_ = conn.Close()
	}
}

func (d *Daemon) processEvent(ev event.Event) error {
	// Auto-create session for any event with a session ID.
	// For historical events (>2h old, e.g. from log watcher), mark session as ended
	// immediately so they don't appear as "running".
	if ev.SessionID != "" && ev.Type != event.EventSessionEnd {
		if err := d.db.UpsertSession(ev.SessionID, ev.Platform, ev.Timestamp); err != nil {
			log.Printf("upsert session %s: %v", ev.SessionID, err)
		}
		// Any event older than 2h is from a historical log replay — end the session
		// so it doesn't linger as "active". Previously only EventTokenUsage triggered
		// this, leaving sessions without token rows permanently active.
		if time.Since(ev.Timestamp) > 2*time.Hour {
			if err := d.db.EndSession(ev.SessionID, ev.Timestamp); err != nil {
				log.Printf("end historical session %s: %v", ev.SessionID, err)
			}
		}
	}

	switch ev.Type {
	case event.EventSessionStart:
		if ev.Data.CWD != "" || ev.Data.GitBranch != "" {
			if err := d.db.UpdateSessionMeta(ev.SessionID, ev.Data.CWD, ev.Data.GitBranch); err != nil {
				log.Printf("update session meta %s: %v", ev.SessionID, err)
			}
		}
		return nil

	case event.EventSessionUpdate:
		if ev.Data.CWD != "" || ev.Data.GitBranch != "" {
			if err := d.db.FillSessionMeta(ev.SessionID, ev.Data.CWD, ev.Data.GitBranch); err != nil {
				log.Printf("fill session meta %s: %v", ev.SessionID, err)
			}
		}
		if ev.Platform == event.PlatformCodex && ev.Data.Model != "" {
			return d.reconcileCodexTokenModel(ev.SessionID, ev.Data.Model, ev.Timestamp, true)
		}
		return nil

	case event.EventSessionEnd:
		canEnd, err := d.db.CanEndSession(ev.SessionID, ev.Timestamp)
		if err != nil {
			return err
		}
		if !canEnd {
			return nil
		}
		if err := d.db.MarkPendingToolCallsInterrupted(ev.SessionID); err != nil {
			return err
		}
		return d.db.EndSession(ev.SessionID, ev.Timestamp)

	case event.EventAgentStart:
		return d.db.UpsertAgent(ev.AgentID, ev.SessionID, ev.Data.ParentAgentID, ev.Data.AgentRole, ev.Timestamp)

	case event.EventAgentEnd:
		return d.db.EndAgent(ev.AgentID, ev.Timestamp)

	case event.EventToolCallStart:
		inserted, err := d.db.InsertToolCallStart(ev.ID, ev.AgentID, ev.SessionID, ev.Data.ToolName, ev.Data.ToolParams, ev.Timestamp)
		if err != nil {
			return err
		}
		if !inserted {
			// Pre re-emit for an existing call_id — Claude doesn't normally
			// do this, but if it ever does we want to know how often before
			// switching to ON CONFLICT DO UPDATE semantics.
			d.duplicateToolStarts.Add(1)
		}
		return nil

	case event.EventToolCallEnd:
		if err := d.db.UpdateToolCallEnd(ev.ID, ev.Data.ToolResult, ev.Data.ToolStatus, ev.Data.DurationMs, ev.Timestamp); err != nil {
			return err
		}
		if ev.Data.FilePath != "" {
			return d.db.InsertFileChangeWithSource(ev.SessionID, ev.Data.FilePath, ev.Data.ChangeType, ev.Timestamp, ev.ID)
		}
		return nil

	case event.EventTokenUsage:
		if ev.Data.GitBranch != "" || ev.Data.CWD != "" {
			if err := d.db.FillSessionMeta(ev.SessionID, ev.Data.CWD, ev.Data.GitBranch); err != nil {
				log.Printf("fill session meta %s: %v", ev.SessionID, err)
			}
		}
		if err := d.db.InsertTokenUsage(ev.AgentID, ev.SessionID, ev.Data.InputTokens, ev.Data.OutputTokens, ev.Data.CacheCreationTokens, ev.Data.CacheReadTokens, ev.Data.Model, ev.Data.CostUSD, ev.Timestamp, ev.ID); err != nil {
			return err
		}
		// Backfill earlier rows in this session that had no model (Codex token_count
		// can arrive before turn_context which carries the model name).
		if ev.Platform == event.PlatformCodex && ev.Data.Model != "" {
			return d.reconcileCodexTokenModel(ev.SessionID, ev.Data.Model, ev.Timestamp, false)
		}
		return nil

	case event.EventFileChange:
		return d.db.InsertFileChangeWithSource(ev.SessionID, ev.Data.FilePath, ev.Data.ChangeType, ev.Timestamp, ev.ID)
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

// ProcessExternalEventAsync sends an event to the batch channel for async
// processing. Returns silently if the daemon is shutting down — the event is
// counted in droppedShutdownEvts so the count appears in the Stop() log.
// (Going synchronous on shutdown is unsafe: db.Close may have already run.)
func (d *Daemon) ProcessExternalEventAsync(ev event.Event) {
	// Try non-blocking send first so a healthy daemon doesn't lose events to
	// the random-pick semantics of the two-case select on done close.
	select {
	case d.batchCh <- ev:
		return
	default:
	}
	// Channel full — block until either consumer drains or done closes.
	select {
	case d.batchCh <- ev:
	case <-d.done:
		d.droppedShutdownEvts.Add(1)
	}
}

// Stats returns a snapshot of observability counters. Safe to call concurrently.
func (d *Daemon) Stats() (droppedBroadcasts, droppedShutdownEvts, duplicateToolStarts int64) {
	return d.droppedBroadcasts.Load(), d.droppedShutdownEvts.Load(), d.duplicateToolStarts.Load()
}

func (d *Daemon) batchConsumer() {
	defer close(d.batchDone)
	for {
		select {
		case ev, ok := <-d.batchCh:
			if !ok {
				return
			}
			if err := d.processEvent(ev); err != nil {
				log.Printf("batch process event: %v", err)
			}
			d.broadcast(ev)
		case <-d.done:
			// Drain remaining events before exiting.
			for {
				select {
				case ev := <-d.batchCh:
					if err := d.processEvent(ev); err != nil {
						log.Printf("batch drain event: %v", err)
					}
					d.broadcast(ev)
				default:
					return
				}
			}
		}
	}
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
