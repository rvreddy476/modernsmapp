package main

import (
	"reflect"
	"testing"
)

// The bug this guards: hashtags were derived by regex over post text
// alone, so a post whose tags live only in posts.hashtags (written by the
// composer's structured tag field) indexed with no hashtags at all and was
// unfindable through search while /v1/hashtags/* returned it happily.
func TestPostHashtags(t *testing.T) {
	tests := []struct {
		name   string
		stored []string
		text   string
		want   []string
	}{
		{
			name:   "column only — the case that was silently dropped",
			stored: []string{"momentum", "worker"},
			text:   "Worker-published reel proof",
			want:   []string{"momentum", "worker"},
		},
		{
			name:   "text only still works",
			stored: nil,
			text:   "cat test #fun",
			want:   []string{"fun"},
		},
		{
			name:   "column leads, inline text tags union in behind it",
			stored: []string{"momentum"},
			text:   "launch day #reel #momentum",
			want:   []string{"momentum", "reel"},
		},
		{
			name:   "normalized: case, whitespace and a leading hash",
			stored: []string{" #Momentum ", "WORKER"},
			text:   "",
			want:   []string{"momentum", "worker"},
		},
		{
			name:   "duplicates across both sources collapse to one bucket",
			stored: []string{"reel", "reel"},
			text:   "#Reel #reel",
			want:   []string{"reel"},
		},
		{
			name:   "empty and blank entries are dropped, not indexed as \"\"",
			stored: []string{"", "  ", "#"},
			text:   "no tags here",
			want:   nil,
		},
		{
			name:   "no tags anywhere yields nil so the field is omitted",
			stored: nil,
			text:   "plain post",
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := postHashtags(tc.stored, tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("postHashtags(%q, %q) = %#v, want %#v",
					tc.stored, tc.text, got, tc.want)
			}
		})
	}
}
