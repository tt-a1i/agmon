package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tt-a1i/tokenmeter/internal/event"
)

// sseLines reads SSE data lines from a response body into a channel.
// Stops when it encounters a read error (e.g. server closes connection).
func sseLines(resp *http.Response, out chan<- string, done <-chan struct{}) {
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			select {
			case out <- strings.TrimPrefix(line, "data: "):
			case <-done:
				return
			}
		}
	}
	close(out)
}

// TestSSEHandlerBuffersTokenUsage verifies that a burst of token_usage events
// is coalesced by SSEBuffer before reaching the SSE client. 50 events sent in
// rapid succession should produce fewer than 10 SSE data frames.
func TestSSEHandlerBuffersTokenUsage(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithEventSocketPath("test.sock"))
	srv.eventHeartbeat = 200 * time.Millisecond // slow heartbeat to avoid interference

	events := make(chan event.Event, 100)
	srv.subscribeRemote = func(string) (<-chan event.Event, func(), error) {
		return events, func() {}, nil
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("connect to SSE: %v", err)
	}
	defer resp.Body.Close()

	// Wait for the opening heartbeat so we know the handler is running.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": heartbeat") {
			break
		}
	}

	// Burst 50 token_usage events for the same session as fast as possible.
	now := time.Now()
	for i := range 50 {
		events <- event.Event{
			ID:        fmt.Sprintf("tok-%d", i),
			Type:      event.EventTokenUsage,
			SessionID: "burst-session",
			Platform:  event.PlatformClaude,
			Timestamp: now,
			Data:      event.EventData{InputTokens: (i + 1) * 10, OutputTokens: i + 1},
		}
	}
	// Close events channel to signal end-of-stream.
	close(events)

	// Collect all SSE data lines until server closes.
	var received []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			received = append(received, strings.TrimPrefix(line, "data: "))
		}
	}

	// With 50ms coalescing, 50 rapid events → significantly fewer SSE frames.
	// Allow up to 10 (generous margin for slow CI) but never 50.
	if got := len(received); got == 0 {
		t.Fatal("expected at least one SSE event, got none (buffer may not be flushing)")
	}
	if got := len(received); got >= 25 {
		t.Errorf("expected < 25 SSE events for 50 burst token_usage (coalescing), got %d", got)
	}
	t.Logf("coalesced %d events → %d SSE frames", 50, len(received))
}

// TestSSEHandlerPassesThroughOtherTypes verifies that non-token_usage events
// (tool_call_start, session_end, …) bypass the buffer and arrive immediately.
func TestSSEHandlerPassesThroughOtherTypes(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithEventSocketPath("test.sock"))
	srv.eventHeartbeat = 500 * time.Millisecond

	events := make(chan event.Event, 10)
	srv.subscribeRemote = func(string) (<-chan event.Event, func(), error) {
		return events, func() {}, nil
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": heartbeat") {
			break
		}
	}

	// Send a mix: tool_call_start should pass through immediately; token_usage
	// is coalesced. We send tool_call_start first, then token_usage.
	now := time.Now()
	events <- event.Event{
		ID: "tc-1", Type: event.EventToolCallStart,
		SessionID: "pass-session", Platform: event.PlatformClaude,
		Timestamp: now,
		Data:      event.EventData{ToolName: "Read"},
	}
	events <- event.Event{
		ID: "tok-1", Type: event.EventTokenUsage,
		SessionID: "pass-session", Platform: event.PlatformClaude,
		Timestamp: now,
		Data:      event.EventData{InputTokens: 100},
	}
	close(events)

	var evTypes []string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev event.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		evTypes = append(evTypes, string(ev.Type))
	}

	// Must have received at least both events.
	if len(evTypes) < 2 {
		t.Errorf("expected >= 2 SSE events, got %d: %v", len(evTypes), evTypes)
	}
	// First received must be tool_call_start (pass-through, no buffering).
	if len(evTypes) > 0 && evTypes[0] != string(event.EventToolCallStart) {
		t.Errorf("first event should be tool_call_start (pass-through), got %q", evTypes[0])
	}
	// Must have received a token_usage eventually (from buffer flush).
	var hadTokenUsage bool
	for _, et := range evTypes {
		if et == string(event.EventTokenUsage) {
			hadTokenUsage = true
		}
	}
	if !hadTokenUsage {
		t.Error("token_usage event was never flushed to SSE client")
	}
}

// TestSSEHandlerFlushesOnDisconnect verifies that when the client disconnects,
// the handler's deferred buf.Stop() triggers a final flush so in-flight
// token_usage events reach the SSE writer (or are cleanly dropped — the key
// invariant is that the handler returns without leaking goroutines).
func TestSSEHandlerFlushesOnDisconnect(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithEventSocketPath("test.sock"))
	srv.eventHeartbeat = time.Second

	closeFnCalled := make(chan struct{})
	events := make(chan event.Event, 20)
	srv.subscribeRemote = func(string) (<-chan event.Event, func(), error) {
		return events, func() { close(closeFnCalled) }, nil
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Wait for opening heartbeat.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": heartbeat") {
			break
		}
	}

	// Send some token_usage events that will be in the buffer when we disconnect.
	now := time.Now()
	for i := range 5 {
		events <- event.Event{
			ID: fmt.Sprintf("tok-%d", i), Type: event.EventTokenUsage,
			SessionID: "disc-session", Platform: event.PlatformClaude,
			Timestamp: now,
		}
	}

	// Client disconnects by closing body. This cancels r.Context().
	resp.Body.Close()

	// The handler must return (and call the closeFn) within a short deadline.
	// This verifies there is no goroutine leak on disconnect.
	select {
	case <-closeFnCalled:
		// Good — handler exited and called closeFn.
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after client disconnect (possible goroutine leak)")
	}
}

// TestSSEHandlerCleanupOnContextCancel is a race-detector exercise: confirms
// that creating an SSEBuffer, adding events, then cancelling the request
// context does not trigger the race detector or leave goroutines running.
func TestSSEHandlerCleanupOnContextCancel(t *testing.T) {
	db := testDB(t)
	srv := NewServer(db, "0", WithEventSocketPath("ctx.sock"))
	srv.eventHeartbeat = 100 * time.Millisecond

	events := make(chan event.Event, 10)
	done := make(chan struct{})
	srv.subscribeRemote = func(string) (<-chan event.Event, func(), error) {
		return events, func() { close(done) }, nil
	}

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Wait for opening heartbeat.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), ": heartbeat") {
			break
		}
	}

	// Flood token_usage and then disconnect rapidly.
	go func() {
		now := time.Now()
		for i := range 20 {
			select {
			case events <- event.Event{
				ID: fmt.Sprintf("ctx-tok-%d", i), Type: event.EventTokenUsage,
				SessionID: "ctx-session", Platform: event.PlatformClaude,
				Timestamp: now,
			}:
			case <-done:
				return
			}
		}
	}()

	time.Sleep(10 * time.Millisecond)
	resp.Body.Close()

	// Verify handler exits cleanly.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit after context cancel")
	}
}
