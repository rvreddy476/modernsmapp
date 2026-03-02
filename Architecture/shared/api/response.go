package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/facebook-like/shared/o11y/trace"
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

// JSON sends a standard success response relative to the context.
// In a real implementation using Gin, this would take *gin.Context.
// Here we use standard http.ResponseWriter for portability if needed, or just helpers.
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

// JSONWithContext is like JSON but automatically populates Meta.RequestID from context.
func JSONWithContext(ctx context.Context, w http.ResponseWriter, status int, data interface{}) {
	meta := &Meta{
		RequestID: trace.RequestIDFrom(ctx),
	}
	JSON(w, status, data, meta)
}

// ErrorWithContext is like Error but automatically populates Meta.RequestID from context.
func ErrorWithContext(ctx context.Context, w http.ResponseWriter, status int, code, message string, details interface{}) {
	meta := &Meta{
		RequestID: trace.RequestIDFrom(ctx),
	}
	Error(w, status, code, message, details, meta)
}
