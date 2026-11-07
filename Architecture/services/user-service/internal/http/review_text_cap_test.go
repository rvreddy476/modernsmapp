package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func bindReview(t *testing.T, body string) error {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var req SubmitReviewRequest
	return c.ShouldBindJSON(&req)
}

func TestReviewTextIsCappedAt2000Characters(t *testing.T) {
	ok := `{"rating":5,"review_text":"` + strings.Repeat("é", 2000) + `"}`
	if err := bindReview(t, ok); err != nil {
		t.Fatalf("2000 characters (multi-byte) must bind: %v", err)
	}
	long := `{"rating":5,"review_text":"` + strings.Repeat("a", 2001) + `"}`
	if err := bindReview(t, long); err == nil {
		t.Fatal("2001 characters must be refused")
	}
}
