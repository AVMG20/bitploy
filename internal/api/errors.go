package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// errBody is the inner object of {"error": {...}}.
type errBody struct {
	Code    string         `json:"code" doc:"Stable snake_case error code shared with MCP"`
	Message string         `json:"message" doc:"What went wrong, in plain words"`
	Hint    string         `json:"hint,omitempty" doc:"The exact fix"`
	Next    string         `json:"next,omitempty" doc:"MCP tool or API action to try next"`
	Details map[string]any `json:"details,omitempty" doc:"Structured extra data (log tail, findings, ...)"`
}

// apiError renders an *apperr.Error the bitploy way:
// {"error":{"code","message","hint","next","details"}}.
type apiError struct {
	status  int
	headers http.Header
	Err     errBody `json:"error"`
}

func (e *apiError) Error() string                  { return e.Err.Code + ": " + e.Err.Message }
func (e *apiError) GetStatus() int                 { return e.status }
func (e *apiError) GetHeaders() http.Header        { return e.headers }
func (e *apiError) ErrorDetail() *huma.ErrorDetail { return &huma.ErrorDetail{Message: e.Err.Message} }

// toHuma converts any error into the apiError huma will write.
func toHuma(err error) error {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*apiError); ok {
		return ae
	}
	e := apperr.As(err)
	status := e.HTTPStatus
	if status == 0 {
		status = http.StatusInternalServerError
	}
	h := http.Header{}
	if status == http.StatusUnauthorized {
		h.Set("WWW-Authenticate", `Bearer realm="bitploy"`)
	}
	if v, ok := e.Details["retry_after_seconds"]; ok && status == http.StatusTooManyRequests {
		h.Set("Retry-After", fmt.Sprint(v))
	}
	return &apiError{
		status: status, headers: h,
		Err: errBody{
			Code: e.Code, Message: apperr.Redact(e.Message), Hint: apperr.Redact(e.Hint),
			Next: e.Next, Details: e.Details,
		},
	}
}

func rateLimited(retryAfter int) *apperr.Error {
	return apperr.TooMany("rate_limited",
		"Too many requests: this token may make 120 requests per minute.",
		"Wait "+strconv.Itoa(retryAfter)+" seconds and try again.").WithDetail("retry_after_seconds", retryAfter)
}

// installHumaErrors makes huma's own errors (validation, bad JSON, body too
// large) use the bitploy error shape.
func installHumaErrors() {
	mk := func(status int, msg string, errs ...error) huma.StatusError {
		code := "invalid_request"
		hint := "Check the request against /api/v1/openapi.json."
		switch status {
		case http.StatusRequestEntityTooLarge:
			code, hint = "body_too_large", "Send a smaller request body. For big folders use create_upload and a zip."
		case http.StatusNotFound:
			code, hint = "not_found", "Check the URL against /api/v1/openapi.json."
		case http.StatusMethodNotAllowed:
			code, hint = "method_not_allowed", "Check the HTTP method against /api/v1/openapi.json."
		case http.StatusUnsupportedMediaType, http.StatusNotAcceptable:
			code, hint = "unsupported_media_type", "Send and accept application/json."
		case http.StatusRequestTimeout:
			code, hint = "request_timeout", "Send the whole request body faster."
		case http.StatusInternalServerError:
			return toHuma(apperr.Internal(fmt.Errorf("%s", msg))).(*apiError)
		}
		var details []map[string]any
		message := msg
		for _, e := range errs {
			if e == nil {
				continue
			}
			d := map[string]any{"message": e.Error()}
			if ed, ok := e.(huma.ErrorDetailer); ok {
				x := ed.ErrorDetail()
				d["message"] = x.Message
				if x.Location != "" {
					d["location"] = x.Location
				}
			}
			details = append(details, d)
		}
		if len(details) > 0 {
			message = "The request is not valid: " + fmt.Sprint(details[0]["message"])
			if loc, ok := details[0]["location"]; ok {
				message += " (" + fmt.Sprint(loc) + ")"
			}
		} else if message == "" {
			message = http.StatusText(status)
		}
		eb := errBody{Code: code, Message: message, Hint: hint}
		if len(details) > 0 {
			eb.Details = map[string]any{"errors": details}
		}
		return &apiError{status: status, headers: http.Header{}, Err: eb}
	}
	huma.NewError = mk
	huma.NewErrorWithContext = func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return mk(status, msg, errs...)
	}
}
