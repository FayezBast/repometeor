package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/FayezBast/repometeor/backend/internal/scoring"
	"github.com/FayezBast/repometeor/backend/internal/service"
)

// maxFeedItems bounds an Atom response. Feed readers poll frequently, so the
// payload has to stay small.
const maxFeedItems = 50

// Atom document types. Using encoding/xml structs rather than string templates
// means every value is escaped by the marshaller, and the document is always
// well-formed.

type atomFeed struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle,omitempty"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Author   atomAuthor  `xml:"author"`
	Rights   string      `xml:"rights,omitempty"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

type atomEntry struct {
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Summary atomContent `xml:"summary"`
	Content atomContent `xml:"content"`
}

type atomContent struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

// handleTrendingFeed serves the Atom feed of current rankings.
func (s *Server) handleTrendingFeed(c *gin.Context) {
	query, problem := parseTrendingQuery(c)
	if problem != nil {
		badRequest(c, problem.detail, problem.fields...)
		return
	}
	if query.Limit > maxFeedItems {
		query.Limit = maxFeedItems
	}

	result, err := s.service.Trending(c.Request.Context(), query)
	if err != nil {
		s.fail(c, "feed query failed", err)
		return
	}

	feed := s.buildFeed(c, result)

	encoded, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		s.fail(c, "feed encoding failed", err)
		return
	}
	document := append([]byte(xml.Header), encoded...)

	sum := sha256.Sum256(document)
	etag := `W/"` + hex.EncodeToString(sum[:8]) + `"`

	if match := c.GetHeader("If-None-Match"); match != "" && strings.Contains(match, etag) {
		c.Header("ETag", etag)
		c.Status(http.StatusNotModified)
		return
	}

	c.Header("ETag", etag)
	c.Header("Cache-Control", "public, max-age=300, stale-while-revalidate=600")
	c.Data(http.StatusOK, "application/atom+xml; charset=utf-8", document)
}

func (s *Server) buildFeed(c *gin.Context, result service.TrendingResult) atomFeed {
	apiBase := s.cfg.API.PublicURL
	webBase := s.cfg.API.WebPublicURL

	selfURL := apiBase + "/v1/feeds/trending.xml"
	if raw := c.Request.URL.RawQuery; raw != "" {
		selfURL += "?" + raw
	}

	title := fmt.Sprintf("RepoMeteor · %s momentum", result.Window)
	if result.Collection != "" {
		title = fmt.Sprintf("RepoMeteor · %s (%s)", collectionTitle(result.Collection), result.Window)
	}

	subtitle := "Repositories gaining momentum, ranked from aggregate snapshots."
	if result.DemoMode {
		subtitle = "DEMO DATA — this RepoMeteor instance is serving deterministic fictional " +
			"repositories, not live GitHub data."
	}

	updated := result.GeneratedAt
	if updated.IsZero() {
		updated = s.clock.Now()
	}

	feed := atomFeed{
		Title:    title,
		Subtitle: subtitle,
		// A stable, filter-specific feed ID so a reader treats each filter
		// combination as its own feed rather than merging them.
		ID:      feedID(apiBase, result),
		Updated: updated.UTC().Format(time.RFC3339),
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: selfURL},
			{Rel: "alternate", Type: "text/html", Href: webBase + "/explore"},
		},
		Author: atomAuthor{Name: "RepoMeteor", URI: webBase},
		Rights: "Aggregate public repository data. RepoMeteor is an independent " +
			"open-source project and is not affiliated with or endorsed by GitHub.",
	}

	for _, item := range result.Items {
		repo := item.Repository
		score := item.Score

		entryURL := fmt.Sprintf("%s/r/%s/%s", webBase, repo.OwnerLogin, repo.Name)

		rank := ""
		if score.Rank != nil {
			rank = fmt.Sprintf("#%d · ", *score.Rank)
		}

		summary := fmt.Sprintf("%sScore %.1f · %s stars (%s in %s) · confidence %s",
			rank,
			round1(score.Score),
			scoring.FormatCount(score.StarsTotal),
			signedCount(score.StarDelta),
			result.Window,
			scoring.ConfidenceLabel(score.Confidence),
		)

		// The body restates the stored explanations so a feed reader shows the
		// same "why" as the website.
		body := summary
		if reasons := reasonTexts(score); len(reasons) > 0 {
			body += "\n\n" + strings.Join(reasons, "\n")
		}
		if repo.Description != "" {
			body = repo.Description + "\n\n" + body
		}
		if result.DemoMode {
			body = "[DEMO DATA — fictional repository]\n\n" + body
		}

		feed.Entries = append(feed.Entries, atomEntry{
			Title: fmt.Sprintf("%s%s", rank, repo.FullName),
			// Entry IDs must be stable for a given repository and ranking
			// generation, so a reader marks an entry read exactly once.
			ID:      entryID(apiBase, repo.FullName, result.Window, updated),
			Updated: updated.UTC().Format(time.RFC3339),
			Links: []atomLink{
				{Rel: "alternate", Type: "text/html", Href: entryURL},
			},
			Summary: atomContent{Type: "text", Body: summary},
			Content: atomContent{Type: "text", Body: body},
		})
	}

	return feed
}

// feedID builds a stable tag URI for a filter combination.
func feedID(apiBase string, result service.TrendingResult) string {
	params := url.Values{}
	params.Set("window", result.Window)
	if result.Collection != "" {
		params.Set("collection", result.Collection)
	}
	return apiBase + "/v1/feeds/trending.xml?" + params.Encode()
}

// entryID builds a stable identifier for one repository in one generation.
func entryID(apiBase, fullName, window string, generatedAt time.Time) string {
	return fmt.Sprintf("%s/v1/repositories/%s#%s@%d",
		apiBase, fullName, window, generatedAt.UTC().Unix())
}

func collectionTitle(collection string) string {
	switch collection {
	case scoring.CollectionRising:
		return "Rising Now"
	case scoring.CollectionDaily:
		return "Daily Momentum"
	case scoring.CollectionWeekly:
		return "Weekly Momentum"
	case scoring.CollectionHiddenGems:
		return "Hidden Gems"
	case scoring.CollectionNewEntrants:
		return "New Entrants"
	case scoring.CollectionEstablished:
		return "Established Movers"
	default:
		return collection
	}
}

func signedCount(delta int) string {
	if delta >= 0 {
		return "+" + scoring.FormatCount(delta)
	}
	return scoring.FormatCount(delta)
}
