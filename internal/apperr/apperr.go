// Package apperr is the one error type used everywhere in bitploy. The REST
// API and the MCP server both render it; nothing else invents error text.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// Error is a user-facing failure: what went wrong, why, and the exact fix.
type Error struct {
	Code       string `json:"code"`           // stable snake_case code shared by API and MCP
	Message    string `json:"message"`        // what went wrong, in plain words
	Hint       string `json:"hint,omitempty"` // the exact fix
	Next       string `json:"next,omitempty"` // MCP tool (or API action) to call next
	HTTPStatus int    `json:"-"`
	// Details carries optional structured data (log tail, findings, template text).
	Details map[string]any `json:"details,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// New builds an error. Use the helpers below for common statuses.
func New(status int, code, message, hint string) *Error {
	return &Error{Code: code, Message: message, Hint: hint, HTTPStatus: status}
}

// WithNext sets the tool the agent should call next.
func (e *Error) WithNext(next string) *Error { e.Next = next; return e }

// WithCause attaches an internal cause (never shown to users).
func (e *Error) WithCause(err error) *Error { e.cause = err; return e }

// WithDetail adds one structured detail.
func (e *Error) WithDetail(k string, v any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[k] = v
	return e
}

func BadRequest(code, message, hint string) *Error {
	return New(http.StatusBadRequest, code, message, hint)
}
func Unauthorized(code, message, hint string) *Error {
	return New(http.StatusUnauthorized, code, message, hint)
}
func Forbidden(code, message, hint string) *Error {
	return New(http.StatusForbidden, code, message, hint)
}
func NotFound(code, message, hint string) *Error {
	return New(http.StatusNotFound, code, message, hint)
}
func Conflict(code, message, hint string) *Error {
	return New(http.StatusConflict, code, message, hint)
}
func Unprocessable(code, message, hint string) *Error {
	return New(http.StatusUnprocessableEntity, code, message, hint)
}
func TooMany(code, message, hint string) *Error {
	return New(http.StatusTooManyRequests, code, message, hint)
}
func Unavailable(code, message, hint string) *Error {
	return New(http.StatusServiceUnavailable, code, message, hint)
}

// Internal wraps an unexpected error. The cause is logged, never returned.
func Internal(err error) *Error {
	return New(http.StatusInternalServerError, "internal_error",
		"Something went wrong inside bitploy.",
		"Try again in a minute. If it keeps happening, tell the bitploy admin.").WithCause(err)
}

// NotImplemented marks a stub that is not built yet.
func NotImplemented(what string) *Error {
	return New(http.StatusNotImplemented, "not_implemented", what+" is not implemented yet.", "")
}

// As converts any error into an *Error, wrapping unknown errors as internal.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

var tokenRe = regexp.MustCompile(`bp_[A-Za-z0-9]{20,}`)
var ghTokenRe = regexp.MustCompile(`(github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,})`)

// Redact removes bitploy and GitHub tokens from any text before it is logged,
// stored in a build log or returned in an error.
func Redact(s string) string {
	s = tokenRe.ReplaceAllString(s, "bp_[REDACTED]")
	return ghTokenRe.ReplaceAllString(s, "[REDACTED]")
}
