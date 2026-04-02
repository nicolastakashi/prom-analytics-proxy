package models

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	TraceID string `json:"traceId,omitempty"`
}
