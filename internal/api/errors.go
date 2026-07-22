package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// APIError is the Celeris error envelope {"error":{message,type,code}}
// plus transport-level context.
type APIError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	RetryAfter string // seconds, present on 429 responses
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	s := fmt.Sprintf("%s (HTTP %d", msg, e.StatusCode)
	if e.Code != "" {
		s += ", " + e.Code
	}
	s += ")"
	if e.RetryAfter != "" {
		s += fmt.Sprintf("; retry after %ss", e.RetryAfter)
	}
	return s
}

func parseAPIError(resp *http.Response, body []byte) error {
	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		RetryAfter: resp.Header.Get("Retry-After"),
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		apiErr.Message = envelope.Error.Message
		apiErr.Type = envelope.Error.Type
		apiErr.Code = envelope.Error.Code
	} else if s := strings.TrimSpace(string(body)); s != "" && len(s) < 512 {
		apiErr.Message = s
	}
	return apiErr
}
