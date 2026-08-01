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

	repo := Repository{
		ID:              p.ID,
		NodeID:          p.NodeID,
		Owner:           owner,
		Name:            name,
		FullName:        fullName,
		Description:     strings.TrimSpace(p.Description),
		HTMLURL:         p.HTMLURL,
		HomepageURL:     strings.TrimSpace(p.Homepage),
		AvatarURL:       p.Owner.AvatarURL,
		DefaultBranch:   p.DefaultBranch,
		PrimaryLanguage: p.Language,
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
		repo.LicenseSPDX = p.License.SPDXID
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
