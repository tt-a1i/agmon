package report

import (
	"testing"

	"github.com/tt-a1i/tokenmeter/internal/storage"
)

func TestSessionName(t *testing.T) {
	cases := []struct {
		name string
		row  storage.SessionRow
		want string
	}{
		{
			name: "cwd + git_branch → base+branch",
			row:  storage.SessionRow{CWD: "/Users/me/proj/x", GitBranch: "feat"},
			want: "x/feat",
		},
		{
			name: "git_branch only",
			row:  storage.SessionRow{GitBranch: "main"},
			want: "main",
		},
		{
			name: "cwd only → basename",
			row:  storage.SessionRow{CWD: "/Users/me/proj/x"},
			want: "x",
		},
		{
			name: "fallback to short session id",
			row:  storage.SessionRow{SessionID: "abcdef1234567890-extra"},
			want: "abcdef123456",
		},
		{
			name: "short session id passes through",
			row:  storage.SessionRow{SessionID: "abc"},
			want: "abc",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sessionName(c.row); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
