// Package api implements the RepoMeteor HTTP API.
//
// Every response is JSON with camelCase fields; every error is
// application/problem+json (RFC 9457). Handlers validate all input before it
// reaches the service layer, and no handler ever interpolates request data into
// SQL, XML or SVG without escaping.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ProblemType identifies an error class. The values are stable relative URIs
// documented in docs/API.md, so clients can branch on them.
type ProblemType string

const (
	ProblemValidation    ProblemType = "/problems/validation-failed"
	ProblemNotFound      ProblemType = "/problems/not-found"
	ProblemRateLimited   ProblemType = "/problems/rate-limited"
	ProblemUpstream      ProblemType = "/problems/upstream-unavailable"
	ProblemInternal      ProblemType = "/problems/internal-error"
	ProblemNotReady      ProblemType = "/problems/not-ready"
	ProblemUnsupported   ProblemType = "/problems/unsupported"
	ProblemPayloadTooBig ProblemType = "/problems/payload-too-large"
)

// Problem is an RFC 9457 problem detail.
type Problem struct {
	Type   ProblemType `json:"type"`
	Title  string      `json:"title"`
	Status int         `json:"status"`
	Detail string      `json:"detail,omitempty"`
	// Instance is the request path that produced the problem.
	Instance string `json:"instance,omitempty"`
	// RequestID lets a user quote one identifier when reporting an issue.
	RequestID string `json:"requestId,omitempty"`
	// Errors carries per-field validation messages.
	Errors []FieldError `json:"errors,omitempty"`
	// RetryAfterSeconds is set on rate-limit and upstream problems.
	RetryAfterSeconds int `json:"retryAfterSeconds,omitempty"`
}

// FieldError is one validation failure.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// problemContentType is the media type required by RFC 9457.
const problemContentType = "application/problem+json"

// writeProblem renders a problem detail and aborts the handler chain.
func writeProblem(c *gin.Context, problem Problem) {
	problem.Instance = c.Request.URL.Path
	problem.RequestID = RequestIDFrom(c)
	if problem.Title == "" {
		problem.Title = http.StatusText(problem.Status)
	}

	// Errors must never be cached for long: a 404 today may be a 200 tomorrow
	// once collection catches up.
	c.Header("Cache-Control", "no-store")
	if problem.RetryAfterSeconds > 0 {
		c.Header("Retry-After", itoa(problem.RetryAfterSeconds))
	}

	c.Abort()
	c.Data(problem.Status, problemContentType, mustMarshal(problem))
}

// badRequest reports invalid input with per-field detail.
func badRequest(c *gin.Context, detail string, fields ...FieldError) {
	writeProblem(c, Problem{
		Type:   ProblemValidation,
		Title:  "Validation failed",
		Status: http.StatusBadRequest,
		Detail: detail,
		Errors: fields,
	})
}

// notFound reports a missing resource.
func notFound(c *gin.Context, detail string) {
	writeProblem(c, Problem{
		Type:   ProblemNotFound,
		Title:  "Not found",
		Status: http.StatusNotFound,
		Detail: detail,
	})
}

// rateLimited reports that the caller has exceeded its allowance.
func rateLimited(c *gin.Context, detail string, retryAfterSeconds int) {
	writeProblem(c, Problem{
		Type:              ProblemRateLimited,
		Title:             "Too many requests",
		Status:            http.StatusTooManyRequests,
		Detail:            detail,
		RetryAfterSeconds: retryAfterSeconds,
	})
}

// upstreamUnavailable reports that GitHub capacity is exhausted.
func upstreamUnavailable(c *gin.Context, detail string, retryAfterSeconds int) {
	writeProblem(c, Problem{
		Type:              ProblemUpstream,
		Title:             "Upstream capacity unavailable",
		Status:            http.StatusServiceUnavailable,
		Detail:            detail,
		RetryAfterSeconds: retryAfterSeconds,
	})
}

// internalError reports an unexpected failure.
//
// The detail is a fixed string in every environment: an internal error message
// can carry a query fragment, a hostname or a stack frame, none of which belong
// in a public response. The request ID is the bridge to the server logs, which
// hold the real cause.
func internalError(c *gin.Context) {
	writeProblem(c, Problem{
		Type:   ProblemInternal,
		Title:  "Internal server error",
		Status: http.StatusInternalServerError,
		Detail: "The request could not be completed. Quote the request ID when reporting this.",
	})
}

// notReady reports that a dependency is unavailable.
func notReady(c *gin.Context, detail string) {
	writeProblem(c, Problem{
		Type:   ProblemNotReady,
		Title:  "Service not ready",
		Status: http.StatusServiceUnavailable,
		Detail: detail,
	})
}
