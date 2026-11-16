package service

import (
	"errors"
	"fmt"
	"net/http"
)

// UpstreamStatusError is a dependency's non-200 answer to a call made while
// applying an event (the graph and post-service reads of the fan-out, the
// unfollow purge's relationship lookup). It carries the status so the event
// consumer can tell a dependency that is DOWN (retry until it is back) from
// one that REFUSED this exact request (retrying sends the same request and
// gets the same refusal). Its text is what these calls returned before the
// type existed.
type UpstreamStatusError struct {
	Service string // "graph-service", "post-service"
	Status  int
	Body    string
}

func (e *UpstreamStatusError) Error() string {
	return fmt.Sprintf("%s returned %d: %s", e.Service, e.Status, e.Body)
}

// Rejected reports whether the status means "this request is wrong and
// always will be": 400 (for one, graph-service's OFFSET_TOO_LARGE past
// offset 10,000), 413, 414 and 422.
//
// Everything else is treated as an outage and retried, on purpose:
// 401 / 403 are a missing or rotated internal key and 404 is a route that is
// not there (a deploy in progress, a wrong base URL), all of which are fixed
// by configuration, after which the same event succeeds; 408, 425, 429 and
// every 5xx are the dependency being slow or down.
func (e *UpstreamStatusError) Rejected() bool {
	switch e.Status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge,
		http.StatusRequestURITooLong, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// UpstreamRejected reports whether err is nothing but refusals: an
// UpstreamStatusError whose status is Rejected, or a join (FanoutPost joins
// the errors of its legs) in which EVERY branch is one. A join that still
// holds one outage is not a refusal: retrying finishes the legs that can
// finish, and their saved progress means only the refused leg is left when
// the event is finally given up on.
func UpstreamRejected(err error) bool {
	if err == nil {
		return false
	}
	if status, ok := err.(*UpstreamStatusError); ok {
		return status.Rejected()
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		branches := joined.Unwrap()
		if len(branches) == 0 {
			return false
		}
		for _, branch := range branches {
			if !UpstreamRejected(branch) {
				return false
			}
		}
		return true
	}
	return UpstreamRejected(errors.Unwrap(err))
}
