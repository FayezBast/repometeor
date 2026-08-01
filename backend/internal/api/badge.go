package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/repometeor/repometeor/backend/internal/scoring"
	"github.com/repometeor/repometeor/backend/internal/service"
)

// Badge themes.
const (
	themeLight = "light"
	themeDark  = "dark"
	themeAuto  = "auto"
)

// badgePalette is one theme's colours.
type badgePalette struct {
	LabelBackground string
	ValueBackground string
	LabelText       string
	ValueText       string
	Border          string
}

var badgeThemes = map[string]badgePalette{
	themeLight: {
		LabelBackground: "#24292f",
		ValueBackground: "#0969da",
		LabelText:       "#ffffff",
		ValueText:       "#ffffff",
		Border:          "#00000022",
	},
	themeDark: {
		LabelBackground: "#010409",
		ValueBackground: "#1f6feb",
		LabelText:       "#e6edf3",
		ValueText:       "#ffffff",
		Border:          "#ffffff22",
	},
}

// handleBadge renders an embeddable SVG badge.
//
// Badges are served to <img> tags in READMEs, which means the response is
// cached aggressively by GitHub's image proxy and can never rely on cookies or
// scripts. Every dynamic value is XML-escaped through encoding/xml rather than
// string concatenation, so a repository description or name can never break out
// of the document.
func (s *Server) handleBadge(c *gin.Context) {
	name, ok := s.repositoryNameParam(c)
	if !ok {
		return
	}

	metric := strings.TrimSpace(c.DefaultQuery("metric", "momentum"))
	switch metric {
	case "momentum", "stars", "delta":
	default:
		// A badge must always render an image: returning a problem document to
		// an <img> tag shows a broken image instead of a message a reader can
		// act on. Invalid input therefore produces a valid "unknown" badge.
		s.writeBadge(c, http.StatusBadRequest, "repometeor", "invalid metric", themeLight, true)
		return
	}

	window := strings.TrimSpace(c.DefaultQuery("window", scoring.Window24h.Name))
	if _, err := scoring.WindowByName(window); err != nil {
		s.writeBadge(c, http.StatusBadRequest, "repometeor", "invalid window", themeLight, true)
		return
	}

	theme := strings.TrimSpace(c.DefaultQuery("theme", themeAuto))
	if theme != themeLight && theme != themeDark && theme != themeAuto {
		theme = themeAuto
	}

	detail, err := s.service.RepositoryDetail(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			s.writeBadge(c, http.StatusNotFound, "repometeor", "not tracked", theme, true)
			return
		}
		s.log.ErrorContext(c.Request.Context(), "badge render failed",
			slogString("error", err.Error()))
		s.writeBadge(c, http.StatusInternalServerError, "repometeor", "unavailable", theme, true)
		return
	}

	score, hasScore := detail.Scores[window]

	label := "momentum"
	value := "no data"

	switch metric {
	case "momentum":
		label = "momentum " + window
		if hasScore {
			value = fmt.Sprintf("%.1f", round1(score.Score))
		}
	case "stars":
		label = "stars"
		if detail.Latest != nil {
			value = scoring.FormatCount(detail.Latest.Stars)
		}
	case "delta":
		label = "stars " + window
		if hasScore {
			if score.StarDelta >= 0 {
				value = "+" + scoring.FormatCount(score.StarDelta)
			} else {
				value = scoring.FormatCount(score.StarDelta)
			}
		}
	}

	s.writeBadge(c, http.StatusOK, label, value, theme, !hasScore && detail.Latest == nil)
}

// writeBadge renders and sends the SVG.
func (s *Server) writeBadge(c *gin.Context, status int, label, value, theme string, unknown bool) {
	svg := renderBadge(label, value, theme, unknown)

	// A weak validator over the rendered bytes lets GitHub's image proxy
	// revalidate cheaply.
	sum := sha256.Sum256(svg)
	etag := `W/"` + hex.EncodeToString(sum[:8]) + `"`

	if match := c.GetHeader("If-None-Match"); match != "" && strings.Contains(match, etag) {
		c.Header("ETag", etag)
		c.Status(http.StatusNotModified)
		return
	}

	c.Header("ETag", etag)
	c.Header("Content-Type", "image/svg+xml; charset=utf-8")
	if status == http.StatusOK {
		c.Header("Cache-Control", "public, max-age=300, stale-while-revalidate=600")
	} else {
		// Do not let a transient "not tracked" state stick in a proxy for long.
		c.Header("Cache-Control", "public, max-age=60")
	}
	c.Data(status, "image/svg+xml; charset=utf-8", svg)
}

// Badge geometry. The character width is an approximation tuned for the 11px
// sans-serif stack; it only needs to be close enough that text never overflows.
const (
	badgeHeight     = 20
	badgeCharWidth  = 6.6
	badgePadding    = 10
	badgeFontSize   = 11
	badgeMinSegment = 40
)

// renderBadge produces a self-contained SVG.
//
// The output references no external fonts, images or stylesheets: a badge that
// fetched anything would leak README readers' requests to a third party and
// would be blocked by GitHub's proxy anyway.
func renderBadge(label, value, theme string, unknown bool) []byte {
	palette := badgeThemes[themeLight]
	if theme == themeDark {
		palette = badgeThemes[themeDark]
	}
	if unknown {
		palette.ValueBackground = "#6e7781"
	}

	labelWidth := segmentWidth(label)
	valueWidth := segmentWidth(value)
	totalWidth := labelWidth + valueWidth

	// The accessible name is what a screen reader announces, so it states the
	// whole badge rather than either half.
	title := fmt.Sprintf("RepoMeteor: %s %s", label, value)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s">`,
		totalWidth, badgeHeight, totalWidth, badgeHeight, escapeXML(title))

	fmt.Fprintf(&b, `<title>%s</title>`, escapeXML(title))

	// Rounded corners via a clip path, which keeps the markup free of any
	// gradient or filter that some renderers drop.
	fmt.Fprintf(&b, `<clipPath id="r"><rect width="%d" height="%d" rx="3" fill="#fff"/></clipPath>`,
		totalWidth, badgeHeight)
	b.WriteString(`<g clip-path="url(#r)">`)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, labelWidth, badgeHeight, palette.LabelBackground)
	fmt.Fprintf(&b, `<rect x="%d" width="%d" height="%d" fill="%s"/>`,
		labelWidth, valueWidth, badgeHeight, palette.ValueBackground)
	b.WriteString(`</g>`)

	fmt.Fprintf(&b, `<g fill="%s" font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif" font-size="%d" text-anchor="middle">`,
		palette.LabelText, badgeFontSize)

	fmt.Fprintf(&b, `<text x="%d" y="14">%s</text>`, labelWidth/2, escapeXML(label))
	fmt.Fprintf(&b, `<text x="%d" y="14" fill="%s">%s</text>`,
		labelWidth+valueWidth/2, palette.ValueText, escapeXML(value))
	b.WriteString(`</g>`)

	// The auto theme adapts through a media query. Both explicit themes ignore
	// it, so a viewer who forces one gets exactly what they asked for.
	if theme == themeAuto {
		dark := badgeThemes[themeDark]
		fmt.Fprintf(&b, `<style>@media (prefers-color-scheme: dark){`+
			`rect:first-of-type{fill:%s}}</style>`, dark.LabelBackground)
	}

	b.WriteString(`</svg>`)
	return []byte(b.String())
}

func segmentWidth(text string) int {
	width := int(float64(len([]rune(text)))*badgeCharWidth) + badgePadding*2
	if width < badgeMinSegment {
		width = badgeMinSegment
	}
	return width
}

// escapeXML escapes a value for inclusion in XML character data or an attribute.
//
// encoding/xml does the escaping rather than a hand-written replacer, so the
// full set of rules — including control characters and the apostrophe — is
// handled by the standard library.
func escapeXML(v string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(v)); err != nil {
		// EscapeText only fails if the writer fails, which strings.Builder
		// never does. Return an empty string rather than unescaped input.
		return ""
	}
	return b.String()
}
