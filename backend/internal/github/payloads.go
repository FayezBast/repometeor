package github

import (
	"errors"
	"strings"
	"time"
)

// repositoryPayload mirrors the subset of GitHub's repository resource that
// RepoMeteor consumes. Fields absent from this struct are never decoded, which
// is a deliberate privacy boundary as much as a performance one.
type repositoryPayload struct {
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"owner"`
	Description   string   `json:"description"`
	HTMLURL       string   `json:"html_url"`
	Homepage      string   `json:"homepage"`
	DefaultBranch string   `json:"default_branch"`
	Language      string   `json:"language"`
	Topics        []string `json:"topics"`
	License       *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`

	StargazersCount  int `json:"stargazers_count"`
	ForksCount       int `json:"forks_count"`
	OpenIssuesCount  int `json:"open_issues_count"`
	SubscribersCount int `json:"subscribers_count"`
	WatchersCount    int `json:"watchers_count"`

	Fork       bool `json:"fork"`
	Archived   bool `json:"archived"`
	Disabled   bool `json:"disabled"`
	IsTemplate bool `json:"is_template"`
	Private    bool `json:"private"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at"`
	PushedAt  *time.Time `json:"pushed_at"`
}

// toRepository converts a decoded payload, rejecting anything structurally
// impossible so that a corrupt response cannot become a corrupt database row.
func (p repositoryPayload) toRepository() (Repository, error) {
	if p.ID <= 0 {
		return Repository{}, errors.New("repository payload has no valid id")
	}

	owner := p.Owner.Login
	name := p.Name
	if owner == "" || name == "" {
		// Fall back to splitting full_name, which some search payloads favour.
		if before, after, ok := strings.Cut(p.FullName, "/"); ok {
			if owner == "" {
				owner = before
			}
			if name == "" {
				name = after
			}
		}
	}
	if owner == "" || name == "" {
		return Repository{}, errors.New("repository payload has no owner or name")
	}

	fullName := p.FullName
	if fullName == "" {
		fullName = owner + "/" + name
	}

	// Identity fields address GitHub's API and build canonical URLs, so a
	// control character in one is not cosmetic damage that can be cleaned up —
	// it means the payload does not describe a repository GitHub could have
	// returned. Reject rather than silently rewrite an identifier.
	if containsControl(owner) || containsControl(name) || containsControl(fullName) {
		return Repository{}, errors.New("repository payload has a control character in its name")
	}

	repo := Repository{
		ID:              p.ID,
		NodeID:          p.NodeID,
		Owner:           owner,
		Name:            name,
		FullName:        fullName,
		Description:     sanitiseDisplayText(p.Description),
		HTMLURL:         p.HTMLURL,
		HomepageURL:     sanitiseDisplayText(p.Homepage),
		AvatarURL:       p.Owner.AvatarURL,
		DefaultBranch:   sanitiseDisplayText(p.DefaultBranch),
		PrimaryLanguage: sanitiseDisplayText(p.Language),
		Topics:          normaliseTopics(p.Topics),
		Stars:           maxInt(p.StargazersCount, 0),
		Forks:           maxInt(p.ForksCount, 0),
		OpenIssues:      maxInt(p.OpenIssuesCount, 0),
		Subscribers:     maxInt(p.SubscribersCount, 0),
		IsFork:          p.Fork,
		IsArchived:      p.Archived,
		IsDisabled:      p.Disabled,
		IsTemplate:      p.IsTemplate,
		IsPrivate:       p.Private,
		CreatedAt:       p.CreatedAt.UTC(),
		UpdatedAt:       utcPtr(p.UpdatedAt),
		PushedAt:        utcPtr(p.PushedAt),
	}

	if p.License != nil && p.License.SPDXID != "" && p.License.SPDXID != "NOASSERTION" {
		repo.LicenseSPDX = sanitiseDisplayText(p.License.SPDXID)
	}
	if repo.HTMLURL == "" {
		repo.HTMLURL = "https://github.com/" + fullName
	}
	// Search results omit subscribers_count; watchers_count is the same number
	// on the full repository resource.
	if repo.Subscribers == 0 && p.WatchersCount > 0 {
		repo.Subscribers = p.WatchersCount
	}

	return repo, nil
}

// sanitiseDisplayText removes characters that let remote text drive a renderer
// rather than merely appear in it.
//
// A description or homepage is free-form text written by whoever owns the
// repository, and RepoMeteor echoes it into a terminal via the CLI, into an
// Atom feed and into the web UI. Terminal escape sequences can erase and
// repaint the lines around them — enough to forge a convincing instruction to
// run a command — and bidirectional overrides can reorder text without
// changing it. Both are stripped here, where the value enters RepoMeteor, so
// that no consumer has to remember to do it.
//
// Stripping rather than rejecting is deliberate: a stray control character in
// a description should cost that character, not the repository's place in the
// rankings. Identity fields are held to the stricter rule in toRepository.
func sanitiseDisplayText(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch {
		// Horizontal and vertical whitespace collapses to a space: these values
		// are rendered on one line, and dropping the character outright would
		// run the words on either side together.
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		// C0 controls and DEL, which include ESC — the lead byte of every
		// terminal escape sequence.
		case r < 0x20 || r == 0x7f:
		// C1 controls. U+009B is a single-character CSI that some terminals
		// honour exactly as they honour "ESC [".
		case r >= 0x80 && r <= 0x9f:
		// Bidirectional overrides and isolates: the "Trojan Source" trick, where
		// displayed order differs from stored order.
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		// Zero-width characters and the byte-order mark, which hide content in
		// plain sight.
		case r == 0x200b, r == 0x200c, r == 0x200d, r == 0xfeff:
		default:
			b.WriteRune(r)
		}
	}

	return strings.TrimSpace(collapseSpaces(b.String()))
}

// collapseSpaces reduces runs of spaces left behind by sanitiseDisplayText to a
// single space, so that a multi-line description still reads as one line.
func collapseSpaces(s string) string {
	if !strings.Contains(s, "  ") {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	previousWasSpace := false
	for _, r := range s {
		if r == ' ' {
			if previousWasSpace {
				continue
			}
			previousWasSpace = true
		} else {
			previousWasSpace = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// containsControl reports whether s holds a C0 control character or DEL.
func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// normaliseTopics lower-cases, de-duplicates and bounds the topic list. GitHub
// already restricts topic characters, but the result is rendered into SVG and
// XML, so the shape is pinned here rather than trusted downstream.
func normaliseTopics(topics []string) []string {
	if len(topics) == 0 {
		return []string{}
	}

	const maxTopics = 24
	seen := make(map[string]struct{}, len(topics))
	out := make([]string, 0, min(len(topics), maxTopics))

	for _, topic := range topics {
		topic = strings.ToLower(strings.TrimSpace(topic))
		if topic == "" || len(topic) > 50 {
			continue
		}
		if !isSafeTopic(topic) {
			continue
		}
		if _, dup := seen[topic]; dup {
			continue
		}
		seen[topic] = struct{}{}
		out = append(out, topic)
		if len(out) == maxTopics {
			break
		}
	}
	return out
}

func isSafeTopic(topic string) bool {
	for _, r := range topic {
		isAllowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_'
		if !isAllowed {
			return false
		}
	}
	return true
}

type releasePayload struct {
	TagName     string     `json:"tag_name"`
	PublishedAt *time.Time `json:"published_at"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
}

type searchPayload struct {
	TotalCount        int                 `json:"total_count"`
	IncompleteResults bool                `json:"incomplete_results"`
	Items             []repositoryPayload `json:"items"`
}

type rateLimitResource struct {
	Limit     int   `json:"limit"`
	Remaining int   `json:"remaining"`
	Used      int   `json:"used"`
	Reset     int64 `json:"reset"`
}

func (r rateLimitResource) toSnapshot(name string) RateLimitSnapshot {
	if r.Limit == 0 && r.Remaining == 0 && r.Reset == 0 {
		return RateLimitSnapshot{Resource: name}
	}
	return RateLimitSnapshot{
		Resource:  name,
		Limit:     r.Limit,
		Remaining: r.Remaining,
		Used:      r.Used,
		ResetAt:   time.Unix(r.Reset, 0).UTC(),
		Known:     true,
	}
}

type rateLimitPayload struct {
	Resources struct {
		Core    rateLimitResource `json:"core"`
		Search  rateLimitResource `json:"search"`
		GraphQL rateLimitResource `json:"graphql"`
	} `json:"resources"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
