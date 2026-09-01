package app

import (
	"errors"
	"fmt"
	"net/url"
)

type SafeHandoffError struct {
	Code           string
	Phase          string
	Message        string
	StatusCode     int
	Hostname       string
	TargetPath     string
	RedirectOrigin string
	Remediation    string
}

func (e *SafeHandoffError) Error() string {
	if e.StatusCode != 0 && e.TargetPath != "" {
		return fmt.Sprintf("%s (path %s, HTTP %d)", e.Message, e.TargetPath, e.StatusCode)
	}
	if e.TargetPath != "" {
		return fmt.Sprintf("%s (path %s)", e.Message, e.TargetPath)
	}
	return e.Message
}

type ErrorPayload struct {
	SchemaVersion  int    `json:"schema_version"`
	Error          string `json:"error"`
	Code           string `json:"code,omitempty"`
	Phase          string `json:"phase,omitempty"`
	StatusCode     int    `json:"status_code,omitempty"`
	Hostname       string `json:"hostname,omitempty"`
	TargetPath     string `json:"target_path,omitempty"`
	RedirectOrigin string `json:"redirect_origin,omitempty"`
	Remediation    string `json:"remediation,omitempty"`
}

func ErrorPayloadFor(err error) ErrorPayload {
	payload := ErrorPayload{SchemaVersion: 1, Error: err.Error()}
	var safe *SafeHandoffError
	if errors.As(err, &safe) {
		payload.Code = safe.Code
		payload.Phase = safe.Phase
		payload.StatusCode = safe.StatusCode
		payload.Hostname = safe.Hostname
		payload.TargetPath = safe.TargetPath
		payload.RedirectOrigin = safe.RedirectOrigin
		payload.Remediation = safe.Remediation
	}
	return payload
}

func hostnameOnly(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
