package http

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func rankedTestContext(target string) *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", target, nil)
	return c
}

func TestRankedPageCursorRoundTrip(t *testing.T) {
	want, err := uuid.NewUUID()
	if err != nil {
		t.Fatalf("timeuuid: %v", err)
	}
	meta := rankedPageMeta(want.String())
	if meta == nil || meta.NextCursor == "" {
		t.Fatal("next cursor missing")
	}
	limit, before, err := rankedPageParams(rankedTestContext("/?limit=15&cursor=" + meta.NextCursor))
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if limit != 15 || before != want.String() {
		t.Fatalf("limit=%d before=%v want 15,%s", limit, before, want)
	}
}

func TestRankedPageCursorRejectsMalformedVersionTimeAndFuture(t *testing.T) {
	for _, cursor := range []string{
		"not-base64!",
		base64.RawURLEncoding.EncodeToString([]byte("v2:20")),
		base64.RawURLEncoding.EncodeToString([]byte("v1:not-a-uuid")),
		base64.RawURLEncoding.EncodeToString([]byte("v1:" + uuid.New().String())), // v4, not a timeuuid
	} {
		if _, _, err := rankedPageParams(rankedTestContext("/?cursor=" + cursor)); err == nil {
			t.Errorf("cursor %q accepted", cursor)
		}
	}
}

func TestRankedPageMetaOmittedAtEnd(t *testing.T) {
	if got := rankedPageMeta(""); got != nil {
		t.Fatalf("meta=%+v want nil", got)
	}
}
