package service

import (
	"reflect"
	"testing"
)

// Description-derived chapters (2026-09-27): the pure parser behind the
// post detail's `chapters` when nothing was saved.
func TestParseDescriptionChapters(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []ChapterRef
	}{
		{
			name: "three plain lines",
			text: "Welcome!\n0:00 Intro\n1:23 Setup\n12:05 The build\nThanks for watching",
			want: []ChapterRef{{0, "Intro"}, {83000, "Setup"}, {725000, "The build"}},
		},
		{
			name: "hours, dashes, brackets and bullets",
			text: "- [0:00] - Intro\n* (1:23:45) — Deep dive\n1:30:00: Wrap up\r\n",
			want: []ChapterRef{{0, "Intro"}, {5025000, "Deep dive"}, {5400000, "Wrap up"}},
		},
		{
			name: "first must be zero",
			text: "0:30 Late start\n1:00 Two\n2:00 Three",
			want: nil,
		},
		{
			name: "at least three",
			text: "0:00 Intro\n1:00 Outro",
			want: nil,
		},
		{
			name: "must ascend",
			text: "0:00 Intro\n2:00 Two\n1:00 Back in time\n3:00 Four",
			want: nil,
		},
		{
			name: "duplicates do not ascend",
			text: "0:00 Intro\n1:00 Two\n1:00 Again\n3:00 Four",
			want: nil,
		},
		{
			name: "timestamps mid-sentence are prose",
			text: "0:00 Intro\nat 1:00 we start\n2:00 Two\n3:00 Three",
			want: []ChapterRef{{0, "Intro"}, {120000, "Two"}, {180000, "Three"}},
		},
		{
			name: "empty title is skipped",
			text: "0:00 Intro\n1:00\n2:00 Two\n3:00 Three",
			want: []ChapterRef{{0, "Intro"}, {120000, "Two"}, {180000, "Three"}},
		},
		{
			name: "seconds over 59 are not a timestamp",
			text: "0:00 Intro\n1:60 Bad\n2:00 Two\n3:00 Three",
			want: []ChapterRef{{0, "Intro"}, {120000, "Two"}, {180000, "Three"}},
		},
		{name: "empty", text: "", want: nil},
		{name: "no timestamps", text: "Just a description\nwith lines", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDescriptionChapters(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}
