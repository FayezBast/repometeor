package reponame

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAcceptsValidForms(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"plain pair", "orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
		{"https url", "https://github.com/orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
		{"http url", "http://github.com/orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
		{"www host", "https://www.github.com/orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
		{"schemeless host", "github.com/orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
		{"trailing slash", "https://github.com/orbit-labs/signal-kit/", "orbit-labs", "signal-kit"},
		{"dot git suffix", "https://github.com/orbit-labs/signal-kit.git", "orbit-labs", "signal-kit"},
		{"pair dot git", "orbit-labs/signal-kit.git", "orbit-labs", "signal-kit"},
		{"scp remote", "git@github.com:orbit-labs/signal-kit.git", "orbit-labs", "signal-kit"},
		{"surrounding space", "  orbit-labs/signal-kit  ", "orbit-labs", "signal-kit"},
		{"leading slash", "/orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
		{"underscores and dots in repo", "north-star/tool_kit.js", "north-star", "tool_kit.js"},
		{"digits", "user123/repo456", "user123", "repo456"},
		{"mixed case preserved", "Orbit-Labs/Signal-Kit", "Orbit-Labs", "Signal-Kit"},
		{"single character", "a/b", "a", "b"},
		{"explicit https port", "https://github.com:443/orbit-labs/signal-kit", "orbit-labs", "signal-kit"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tc.input, err)
			}
			if got.Owner != tc.wantOwner || got.Repository != tc.wantRepo {
				t.Fatalf("Parse(%q) = %+v, want %s/%s", tc.input, got, tc.wantOwner, tc.wantRepo)
			}
		})
	}
}

func TestParseUnderscoreOwnerIsRejected(t *testing.T) {
	// GitHub logins do not permit underscores; a repository name does.
	if _, err := Parse("north_star/tool"); !errors.Is(err, ErrOwnerChars) {
		t.Fatalf("error = %v, want ErrOwnerChars", err)
	}
}

func TestParseRejectsNonGitHubAndUnsafeInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{"empty", "", ErrEmpty},
		{"whitespace only", "   ", ErrEmpty},
		{"other host", "https://gitlab.com/owner/repo", ErrNotGitHub},
		{"lookalike host", "https://github.com.evil.example/owner/repo", ErrNotGitHub},
		{"subdomain host", "https://raw.github.com/owner/repo", ErrNotGitHub},
		{"file scheme", "file:///etc/passwd", ErrNotGitHub},
		{"ftp scheme", "ftp://github.com/owner/repo", ErrNotGitHub},
		{"embedded credentials", "https://user:pass@github.com/owner/repo", ErrNotGitHub},
		{"non standard port", "https://github.com:8080/owner/repo", ErrNotGitHub},
		{"issues subpath", "https://github.com/owner/repo/issues", ErrExtraPath},
		{"tree subpath", "https://github.com/owner/repo/tree/main", ErrExtraPath},
		{"api path", "https://github.com/api/v3/repos/owner/repo", ErrExtraPath},
		{"query string", "https://github.com/owner/repo?tab=readme", ErrExtraPath},
		{"fragment", "https://github.com/owner/repo#readme", ErrExtraPath},
		{"owner only", "owner", ErrMalformed},
		{"three segments", "a/b/c", ErrExtraPath},
		{"empty owner", "/repo", ErrMalformed},
		{"single dot repo", "owner/.", ErrReservedDot},
		{"double dot repo", "owner/..", ErrReservedDot},
		{"space in name", "owner/re po", ErrRepoChars},
		{"newline injection", "owner/repo\nX-Injected: 1", ErrMalformed},
		{"carriage return", "owner/repo\rmore", ErrMalformed},
		{"null byte", "owner/re\x00po", ErrMalformed},
		{"tab", "owner\t/repo", ErrMalformed},
		{"leading hyphen owner", "-owner/repo", ErrOwnerChars},
		{"trailing hyphen owner", "owner-/repo", ErrOwnerChars},
		{"semicolon", "owner/repo;drop", ErrRepoChars},
		{"quote", "owner/repo'", ErrRepoChars},
		{"asterisk", "owner/*", ErrRepoChars},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse(%q) error = %v, want %v", tc.input, err, tc.want)
			}
		})
	}
}

func TestParseRejectsPathTraversal(t *testing.T) {
	// Both literal and percent-encoded traversal must fail. The encoded form is
	// decoded by url.Parse, so "%2e%2e" arrives as ".." and hits the segment
	// count or reserved-name checks rather than silently escaping the path.
	traversals := []string{
		"https://github.com/../../etc/passwd",
		"https://github.com/owner/../../../secret",
		"../../etc/passwd",
		"owner/../..",
		"https://github.com/%2e%2e/%2e%2e/etc",
		"..%2f..%2fetc",
	}
	for _, input := range traversals {
		t.Run(input, func(t *testing.T) {
			if got, err := Parse(input); err == nil {
				t.Fatalf("Parse(%q) = %+v, want an error", input, got)
			}
		})
	}
}

func TestParseRejectsOverlongInput(t *testing.T) {
	if _, err := Parse(strings.Repeat("a", 600)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("error = %v, want ErrTooLong", err)
	}
	if _, err := Parse(strings.Repeat("a", 40) + "/repo"); !errors.Is(err, ErrOwnerLength) {
		t.Fatalf("error = %v, want ErrOwnerLength", err)
	}
	if _, err := Parse("owner/" + strings.Repeat("a", 101)); !errors.Is(err, ErrRepoLength) {
		t.Fatalf("error = %v, want ErrRepoLength", err)
	}
}

func TestParseAcceptsBoundaryLengths(t *testing.T) {
	if _, err := Parse(strings.Repeat("a", MaxOwnerLength) + "/repo"); err != nil {
		t.Fatalf("39-character owner rejected: %v", err)
	}
	if _, err := Parse("owner/" + strings.Repeat("a", MaxRepoLength)); err != nil {
		t.Fatalf("100-character repository name rejected: %v", err)
	}
}

func TestNameHelpers(t *testing.T) {
	n := MustParse("Orbit-Labs/Signal-Kit")

	if got := n.String(); got != "Orbit-Labs/Signal-Kit" {
		t.Errorf("String() = %q, want Orbit-Labs/Signal-Kit", got)
	}
	if got := n.Lower().String(); got != "orbit-labs/signal-kit" {
		t.Errorf("Lower() = %q, want orbit-labs/signal-kit", got)
	}
	if !n.Equal(MustParse("orbit-labs/signal-kit")) {
		t.Error("Equal() should be case-insensitive")
	}
	if n.Equal(MustParse("orbit-labs/other")) {
		t.Error("Equal() should distinguish different repositories")
	}
}

func TestMustParsePanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse should panic on invalid input")
		}
	}()
	MustParse("not a repo")
}

func TestParseList(t *testing.T) {
	names, err := ParseList("orbit-labs/signal-kit, https://github.com/comet-tools/local-indexer", 2, 4)
	if err != nil {
		t.Fatalf("ParseList() error = %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	if names[1].String() != "comet-tools/local-indexer" {
		t.Fatalf("names[1] = %s, want comet-tools/local-indexer", names[1])
	}
}

func TestParseListEnforcesBounds(t *testing.T) {
	if _, err := ParseList("orbit-labs/signal-kit", 2, 4); err == nil {
		t.Error("expected an error for too few repositories")
	}
	if _, err := ParseList("a/b,c/d,e/f,g/h,i/j", 2, 4); err == nil {
		t.Error("expected an error for too many repositories")
	}
}

func TestParseListRejectsDuplicates(t *testing.T) {
	_, err := ParseList("orbit-labs/signal-kit,Orbit-Labs/Signal-Kit", 2, 4)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("error = %v, want a duplicate error", err)
	}
}

func TestParseListPropagatesItemErrors(t *testing.T) {
	_, err := ParseList("orbit-labs/signal-kit,https://gitlab.com/a/b", 2, 4)
	if err == nil || !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("error = %v, want ErrNotGitHub", err)
	}
}

func TestParseListErrorDoesNotEchoUnboundedInput(t *testing.T) {
	long := strings.Repeat("x", 200)
	_, err := ParseList("a/b,"+long, 1, 4)
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 200 {
		t.Fatalf("error message is unbounded (%d chars): %v", len(err.Error()), err)
	}
}
