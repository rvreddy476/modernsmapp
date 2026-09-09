package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
)

// The error contract for BOTH vote routes.
//
// This is the part a client depends on and cannot see the source of, so it is
// pinned here rather than left to whatever the service happened to return.
// What it replaces: /v1/posts/{id}/poll/vote answered every refusal with one
// code, VOTE_ERROR, and put the pgx error in the message —
//
//	"already voted or invalid option: ERROR: duplicate key value violates
//	 unique constraint \"poll_votes_pkey\" (SQLSTATE 23505)"
//
// — so the web client was matching on the literal "23505" to tell "already
// voted" from "poll closed". Meanwhile /v1/posts/{id}/vote returned 500
// INTERNAL_ERROR for both of those, which are client errors.

func decodeErrorEnvelope(t *testing.T, body string) (code, message string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("response is not an error envelope: %v (%s)", err, body)
	}
	return env.Error.Code, env.Error.Message
}

func writeVoteError(t *testing.T, err error) (status int, code, message string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/posts/x/poll/vote", nil)

	h := &Handler{}
	h.writePollVoteError(c, err)

	code, message = decodeErrorEnvelope(t, rec.Body.String())
	return rec.Code, code, message
}

func TestPollVoteErrorsCarryDistinctStableCodes(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		// The two the web client could previously only tell apart by
		// matching on a SQLSTATE.
		{service.ErrPollAlreadyVoted, 400, "POLL_ALREADY_VOTED"},
		{service.ErrPollEnded, 400, "POLL_ENDED"},

		{service.ErrPollOptionInvalid, 400, "POLL_OPTION_INVALID"},
		{service.ErrPollNotFound, 404, "POLL_NOT_FOUND"},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		status, code, _ := writeVoteError(t, tc.err)
		if status != tc.wantStatus || code != tc.wantCode {
			t.Errorf("%v → %d %s, want %d %s", tc.err, status, code, tc.wantStatus, tc.wantCode)
		}
		if seen[code] {
			t.Errorf("%q is used for more than one cause — a client cannot tell them apart", code)
		}
		seen[code] = true
	}
}

// Wrapping must not defeat the mapping: the service returns these bare today,
// but a caller that adds context with %w should still get its own code rather
// than falling through to 500.
func TestPollVoteErrorsSurviveWrapping(t *testing.T) {
	status, code, _ := writeVoteError(t, fmt.Errorf("cast vote: %w", service.ErrPollEnded))
	if status != 400 || code != "POLL_ENDED" {
		t.Fatalf("wrapped ErrPollEnded → %d %s, want 400 POLL_ENDED", status, code)
	}
}

// An unexpected error is a 500 — and its text does not reach the client. The
// leak this guards against was the whole reason a client had to parse
// database output.
func TestUnexpectedPollVoteErrorDoesNotLeakDatabaseDetail(t *testing.T) {
	raw := errors.New(`already voted or invalid option: ERROR: duplicate key value ` +
		`violates unique constraint "poll_votes_pkey" (SQLSTATE 23505)`)

	status, code, message := writeVoteError(t, raw)
	if status != 500 || code != "INTERNAL_ERROR" {
		t.Fatalf("unexpected error → %d %s, want 500 INTERNAL_ERROR", status, code)
	}
	for _, leak := range []string{"poll_votes_pkey", "SQLSTATE", "23505", "constraint"} {
		if strings.Contains(message, leak) {
			t.Fatalf("response body leaks %q: %s", leak, message)
		}
	}
}
