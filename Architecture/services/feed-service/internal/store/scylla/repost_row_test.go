package scylla

import (
	"testing"

	"github.com/gocql/gocql"
)

// Audit M1: only the reposter's own repost rows are removed on undo.
func TestIsRepostRow(t *testing.T) {
	reposter, other := gocql.TimeUUID(), gocql.TimeUUID()
	repost, post := "repost", "post"
	cases := []struct {
		name   string
		author gocql.UUID
		ct     *string
		want   bool
	}{
		{"reposter's repost row", reposter, &repost, true},
		{"original author's own row", other, &post, false},
		{"someone else's repost", other, &repost, false},
		{"reposter's original post row", reposter, &post, false},
		{"no content type", reposter, nil, false},
	}
	for _, tc := range cases {
		if got := isRepostRow(tc.author, tc.ct, reposter); got != tc.want {
			t.Errorf("%s: isRepostRow = %v, want %v", tc.name, got, tc.want)
		}
	}
}
