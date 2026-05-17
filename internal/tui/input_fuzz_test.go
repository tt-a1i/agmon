package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tt-a1i/tokenmeter/internal/storage"
)

// newFuzzModel builds a minimal Model backed by a file-based DB.
// We use a file-based DB per the existing testModelDB pattern; storage.Open
// is lightweight enough for seed-corpus runs. For long fuzz runs the DB file
// stays in the same TempDir for the whole Fuzz invocation (not per-call).
func newFuzzModel(t *testing.T) *Model {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "fuzz.db"))
	if err != nil {
		t.Fatalf("open fuzz db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m := NewModel(db, make(chan EventMsg, 4))
	m.width = 80
	m.height = 24
	m.splash = false // skip splash so Update reaches main state machine
	m.expandedCalls = make(map[string]bool)
	return &m
}

// applyKey sends a single byte as a tea.KeyMsg, translating control bytes to
// the corresponding special key types understood by Bubbletea.
func applyKey(m *Model, b byte) *Model {
	var km tea.KeyMsg
	switch b {
	case 0x1b:
		km = tea.KeyMsg{Type: tea.KeyEsc}
	case '\n', '\r':
		km = tea.KeyMsg{Type: tea.KeyEnter}
	case '\t':
		km = tea.KeyMsg{Type: tea.KeyTab}
	case 0x7f:
		km = tea.KeyMsg{Type: tea.KeyBackspace}
	case 0x08:
		km = tea.KeyMsg{Type: tea.KeyDelete}
	case 0x03:
		km = tea.KeyMsg{Type: tea.KeyCtrlC}
	case 0x04:
		km = tea.KeyMsg{Type: tea.KeyCtrlD}
	case 0x06:
		km = tea.KeyMsg{Type: tea.KeyCtrlF}
	case 0x2f:
		km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
	case ' ':
		km = tea.KeyMsg{Type: tea.KeySpace}
	default:
		km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(b)}}
	}
	next, _ := m.Update(km)
	nm := next.(Model)
	return &nm
}

// FuzzModelUpdateKeyMsg feeds arbitrary byte sequences as key events to
// Model.Update, verifying that no input combination causes a panic.
func FuzzModelUpdateKeyMsg(f *testing.F) {
	// Navigation keys
	f.Add([]byte("jkjk"))
	f.Add([]byte("ggGG"))
	f.Add([]byte("hjkl"))
	// Tab switching
	f.Add([]byte("1234"))
	f.Add([]byte("tttt"))
	// Esc sequences
	f.Add([]byte{0x1b, 'j', 'k'})
	f.Add([]byte{'g', 'd', 0x1b, 'g', 's'})
	// Filter / search
	f.Add([]byte{'/', 'f', 'o', 'o', '\n'})
	f.Add([]byte{'?', 0x1b, 't', 't'})
	// Enter + expand
	f.Add([]byte{'\n', '\n', '\n'})
	// Empty
	f.Add([]byte{})
	// Mix of control bytes
	f.Add([]byte{0x03, 0x04, 0x06, 0x7f, 0x08})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			t.Skip()
		}
		m := newFuzzModel(t)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Model.Update panicked on key input %q: %v", data, r)
			}
		}()
		for _, b := range data {
			m = applyKey(m, b)
		}
		// Render must also not panic.
		_ = m.View()
	})
}

// FuzzModelUpdateWindowSize feeds arbitrary width/height values to Model.Update
// including zero, negative, and extremely large values.
//
// Inputs with w <= 0 are skipped for View(): lipgloss's word-wrap enters an
// infinite loop at width=0, which is a lipgloss bug rather than a Model bug.
// We still send the WindowSizeMsg to Update() to exercise that path.
func FuzzModelUpdateWindowSize(f *testing.F) {
	f.Add(80, 24)
	f.Add(1, 1)
	f.Add(10000, 10000)
	f.Add(-1, -1)
	f.Add(120, 0)
	f.Add(0, 40)
	f.Add(200, 50)
	f.Add(400, 200)
	f.Add(-1<<10, -1<<10)

	f.Fuzz(func(t *testing.T, w, h int) {
		// Cap dimensions to avoid integer overflow inside lipgloss.
		if w > 1<<16 || h > 1<<16 {
			t.Skip()
		}
		m := newFuzzModel(t)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Model.Update WindowSizeMsg w=%d h=%d panicked: %v", w, h, r)
			}
		}()
		sizeMsg := tea.WindowSizeMsg{Width: w, Height: h}
		next, _ := m.Update(sizeMsg)
		nm := next.(Model)
		// Only call View() when width > 0 to avoid the lipgloss infinite-loop
		// at zero-width word-wrap (a known lipgloss limitation, not a Model bug).
		if nm.width > 0 {
			_ = nm.View()
		}
	})
}

// FuzzModelUpdateMixedSequence dispatches a mix of key events, window resizes,
// tick messages, and event messages based on the low 2 bits of each byte.
// This exercises state-machine transitions that require interleaved event types.
func FuzzModelUpdateMixedSequence(f *testing.F) {
	// Control byte selects msg type, upper bits carry payload.
	f.Add([]byte("k\x00w\x80j"))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})
	f.Add([]byte{0x1b, 0x01, 'j', 0x02, '\n', 0x03})
	f.Add([]byte{0x04, 0x05, 0x06, 0x07, 0x08, 0x09})
	f.Add([]byte{'/', 'h', 'i', '\n', 0x01, 'j'})
	f.Add([]byte{0xff, 0xfe, 0xfd})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			t.Skip()
		}
		m := newFuzzModel(t)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MixedSequence panicked on %q: %v", data, r)
			}
		}()
		for _, b := range data {
			var msg tea.Msg
			switch b & 0x03 {
			case 0:
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(b >> 2)}}
			case 1:
				dim := int(b >> 2)
				msg = tea.WindowSizeMsg{Width: dim, Height: dim}
			case 2:
				// tickMsg drives the refresh/tick cycle (zero-value time.Time is valid).
				msg = tickMsg{}
			case 3:
				msg = EventMsg{}
			}
			next, _ := m.Update(msg)
			nm := next.(Model)
			m = &nm
		}
		_ = m.View()
	})
}

// FuzzModelEnterAndEsc exercises modal / popup state transitions by sending
// alternating Enter / Esc sequences with random filler keys in between.
// This targets expandedCalls map mutations, search popup open/close, and
// filter mode entry/exit.
func FuzzModelEnterAndEsc(f *testing.F) {
	f.Add([]byte{'\n', 0x1b, '\n', 0x1b})
	f.Add([]byte{'\n', '\n', '\n', 0x1b})
	f.Add([]byte{'j', '\n', 'k', 0x1b, 'j', '\n'})
	f.Add([]byte{'/', 'x', '\n', 0x1b})
	f.Add([]byte{'1', '\n', '2', '\n', 0x1b, 0x1b})
	f.Add([]byte{' ', '\n', ' ', '\n', 0x1b})
	f.Add([]byte{})
	// Rapid enter/esc toggle
	f.Add([]byte{'\n', 0x1b, '\n', 0x1b, '\n', 0x1b, '\n', 0x1b})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 128 {
			t.Skip()
		}
		m := newFuzzModel(t)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("EnterAndEsc panicked on %q: %v", data, r)
			}
		}()
		for _, b := range data {
			m = applyKey(m, b)
		}
		_ = m.View()
	})
}
