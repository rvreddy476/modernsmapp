package api

import (
	"encoding/json"
	"net/http"
)

// Response is the standard envelope for all API responses.
type Response struct {
	Data  interface{} `json:"data,omitempty"`
	Error *APIError   `json:"error,omitempty"`
	Meta  *Meta       `json:"meta,omitempty"`
}

// APIError represents a standardized error structure.
type APIError struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Meta contains metadata about the response, such as request ID and pagination.
type Meta struct {
	RequestID  string `json:"request_id,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// JSON sends a standard success response.
func JSON(w http.ResponseWriter, status int, data interface{}, meta *Meta) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{
		Data: data,
		Meta: meta,
	})
}

// Error sends a standard error response.
func Error(w http.ResponseWriter, status int, code string, message string, details interface{}, meta *Meta) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{
		Error: &APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
		Meta: meta,
	})
}
