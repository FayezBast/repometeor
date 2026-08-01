// Package reponame parses and validates user-supplied GitHub repository
// references.
//
// This package is the single trust boundary for repository identifiers. Every
// value that reaches a GitHub API path, a database query, an SVG badge, an Atom
// feed or a canonical URL passes through Parse first. It rejects anything that
// is not a plain owner/name pair so that later code can build API paths from
// validated components instead of fetching a user-supplied URL — which is what
// makes server-side request forgery structurally impossible here.
package reponame

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Name is a validated GitHub owner/repository pair, preserving the exact
// capitalisation supplied by the caller.
type Name struct {
	Owner      string
	Repository string
}

// String renders "owner/repository".
func (n Name) String() string { return n.Owner + "/" + n.Repository }

// Lower returns the case-folded form used for lookups and comparisons.
func (n Name) Lower() Name {
	return Name{Owner: strings.ToLower(n.Owner), Repository: strings.ToLower(n.Repository)}
}

// Equal compares two names case-insensitively, matching GitHub's behaviour.
func (n Name) Equal(other Name) bool {
	return strings.EqualFold(n.Owner, other.Owner) && strings.EqualFold(n.Repository, other.Repository)
}

// Errors returned by Parse. Callers map these onto HTTP problem details.
var (
	ErrEmpty       = errors.New("repository is required")
	ErrTooLong     = errors.New("repository reference is too long")
	ErrNotGitHub   = errors.New("only github.com repositories are supported")
	ErrExtraPath   = errors.New("reference must be a repository root, not a sub-path")
	ErrMalformed   = errors.New("repository must be in the form owner/name")
	ErrOwnerChars  = errors.New("owner may contain only letters, digits and hyphens")
	ErrRepoChars   = errors.New("repository name may contain only letters, digits, hyphens, underscores and dots")
	ErrOwnerLength = errors.New("owner must be between 1 and 39 characters")
	ErrRepoLength  = errors.New("repository name must be between 1 and 100 characters")
	ErrReservedDot = errors.New("repository name may not be \".\" or \"..\"")
)

// Limits mirror GitHub's own constraints.
const (
	MaxOwnerLength = 39
	MaxRepoLength  = 100
	maxInputLength = 512
)

// allowedGitHubHosts are the only hosts accepted in URL form. www.github.com is
// included because it is what a browser address bar sometimes produces.
var allowedGitHubHosts = map[string]struct{}{
	"github.com":     {},
	"www.github.com": {},
}

// Parse accepts either "owner/name" or a github.com repository URL and returns
// a validated Name.
//
// Accepted:
//
//	orbit-labs/signal-kit
//	https://github.com/orbit-labs/signal-kit
//	https://github.com/orbit-labs/signal-kit.git
//	github.com/orbit-labs/signal-kit
//
// Rejected: other hosts, other schemes, sub-paths such as /issues or /tree/main,
// path traversal, embedded credentials, query strings, control characters and
// anything containing whitespace or newlines.
func Parse(input string) (Name, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Name{}, ErrEmpty
	}
	if len(raw) > maxInputLength {
		return Name{}, ErrTooLong
	}
	// Reject control characters outright. This blocks header and log injection
	// before any other interpretation happens.
	if containsControl(raw) {
		return Name{}, ErrMalformed
	}

	if looksLikeURL(raw) {
		return parseURL(raw)
	}
	return parsePair(raw)
}

func looksLikeURL(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "github.com/") ||
		strings.HasPrefix(lower, "www.github.com/") ||
		strings.HasPrefix(lower, "git@")
}

func parseURL(raw string) (Name, error) {
	candidate := raw

	// scp-style git remotes: git@github.com:owner/name.git
	if rest, ok := strings.CutPrefix(strings.ToLower(candidate), "git@"); ok {
		host, path, found := strings.Cut(rest, ":")
		if !found {
			return Name{}, ErrMalformed
		}
		if _, allowed := allowedGitHubHosts[host]; !allowed {
			return Name{}, ErrNotGitHub
		}
		// Re-slice the original so capitalisation survives.
		offset := len(candidate) - len(path)
		return parsePair(strings.TrimSuffix(candidate[offset:], ".git"))
	}

	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}

	u, err := url.Parse(candidate)
	if err != nil {
		return Name{}, ErrMalformed
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return Name{}, ErrNotGitHub
	}
	// Embedded credentials are never legitimate here and would be a phishing or
	// SSRF signal.
	if u.User != nil {
		return Name{}, ErrNotGitHub
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return Name{}, ErrExtraPath
	}
	if _, allowed := allowedGitHubHosts[strings.ToLower(u.Hostname())]; !allowed {
		return Name{}, ErrNotGitHub
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return Name{}, ErrNotGitHub
	}

	// url.Parse already percent-decoded the path; using EscapedPath would let
	// "%2e%2e" survive as literal dots after a later decode.
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return Name{}, ErrMalformed
	}
	return parsePair(strings.TrimSuffix(path, ".git"))
}

func parsePair(raw string) (Name, error) {
	trimmed := strings.Trim(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return Name{}, ErrEmpty
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		if len(parts) > 2 {
			return Name{}, ErrExtraPath
		}
		return Name{}, ErrMalformed
	}

	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")

	if err := validateOwner(owner); err != nil {
		return Name{}, err
	}
	if err := validateRepository(repo); err != nil {
		return Name{}, err
	}
	return Name{Owner: owner, Repository: repo}, nil
}

func validateOwner(owner string) error {
	if owner == "" || len(owner) > MaxOwnerLength {
		return ErrOwnerLength
	}
	// GitHub logins: alphanumerics and single hyphens, not leading or trailing.
	if strings.HasPrefix(owner, "-") || strings.HasSuffix(owner, "-") {
		return ErrOwnerChars
	}
	for _, r := range owner {
		if !isAlphaNumeric(r) && r != '-' {
			return ErrOwnerChars
		}
	}
	return nil
}

func validateRepository(repo string) error {
	if repo == "" || len(repo) > MaxRepoLength {
		return ErrRepoLength
	}
	if repo == "." || repo == ".." {
		return ErrReservedDot
	}
	for _, r := range repo {
		if !isAlphaNumeric(r) && r != '-' && r != '_' && r != '.' {
			return ErrRepoChars
		}
	}
	return nil
}

func isAlphaNumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// MustParse is a test helper that panics on invalid input.
func MustParse(input string) Name {
	n, err := Parse(input)
	if err != nil {
		panic(fmt.Sprintf("reponame.MustParse(%q): %v", input, err))
	}
	return n
}

// ParseList parses a comma-separated list of references, rejecting duplicates
// (compared case-insensitively) and enforcing a count range.
func ParseList(input string, minCount, maxCount int) ([]Name, error) {
	parts := strings.Split(input, ",")
	names := make([]Name, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, err := Parse(part)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", truncate(part, 60), err)
		}
		key := name.Lower().String()
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s is listed more than once", name)
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}

	if len(names) < minCount {
		return nil, fmt.Errorf("at least %d repositories are required", minCount)
	}
	if len(names) > maxCount {
		return nil, fmt.Errorf("at most %d repositories are allowed", maxCount)
	}
	return names, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
