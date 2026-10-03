package moderation

import "testing"

func TestUnkind(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
		cat  string
	}{
		{"You are such an idiot", true, UnkindInsult},
		{"IDIOT!", true, UnkindInsult},
		{"tu pagal hai kya", true, UnkindInsult},
		{"what a bitch", true, UnkindSlur},
		{"send nudes?", true, UnkindSexual},
		{"I loved your answer about Goa", false, ""},
		{"Hi! Want to get coffee on Saturday?", false, ""},
		// Whole words only: no match inside other words.
		{"I studied at Kochi", false, ""},
		{"sluttery-free zone? no: 'scunthorpe' style words stay clean", false, ""},
		{"", false, ""},
	} {
		got, cats := Unkind(tc.text)
		if got != tc.want {
			t.Errorf("Unkind(%q) = %v %v, want %v", tc.text, got, cats, tc.want)
			continue
		}
		if tc.cat != "" && (len(cats) == 0 || cats[0] != tc.cat) {
			t.Errorf("Unkind(%q) categories = %v, want %s", tc.text, cats, tc.cat)
		}
	}
}
