package titlematch

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func catalogue() (*Matcher, map[string]uuid.UUID) {
	ids := map[string]uuid.UUID{
		"jailer2": uuid.New(), "pokemon": uuid.New(), "oceans": uuid.New(),
		"kgf": uuid.New(), "tamil": uuid.New(), "ninetysix": uuid.New(),
	}
	return New([]Entry{
		{ID: ids["jailer2"], Title: "Jailer 2", Aliases: []string{"Jailer II"}},
		{ID: ids["pokemon"], Title: "Pokémon"},
		{ID: ids["oceans"], Title: "Ocean's Eleven"},
		{ID: ids["kgf"], Title: "K.G.F Chapter 2", Aliases: []string{"KGF 2", "  "}},
		{ID: ids["tamil"], Title: "விக்ரம்"},
		{ID: ids["ninetysix"], Title: "96"},
	}), ids
}

func TestTokens_FoldsCaseAccentsAndSeparators(t *testing.T) {
	for in, want := range map[string][]string{
		"JAILER 2":           {"jailer", "2"},
		"jailer-2":           {"jailer", "2"},
		"Jailer_2!!":         {"jailer", "2"},
		"#Jailer2":           {"jailer2"},
		"Pokémon":            {"pokemon"},
		"POKÉMON":           {"pokemon"},
		"Ocean’s Eleven":     {"oceans", "eleven"},
		"Straße":             {"strasse"},
		"ＪＡＩＬＥＲ":             {"jailer"},
		"விக்ரம் full movie": {"விக்ரம்", "full", "movie"},
		"":                   nil,
		"  --  ":             nil,
		"K.G.F":              {"kgf"},
		"Plan B 2":           {"plan", "b", "2"},
	} {
		if got := Tokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Tokens(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokens_KeepIndicVowelSigns(t *testing.T) {
	// The vowel sign and virama are part of the word: stripping them would
	// make two different Tamil words equal.
	if got := Tokens("விக்ரம்"); len(got) != 1 || got[0] == "விகரம" {
		t.Fatalf("Tamil vowel signs were stripped: %q", got)
	}
	if reflect.DeepEqual(Tokens("कमल"), Tokens("कमाल")) {
		t.Fatal("Devanagari vowel sign folded away")
	}
}

func TestMatchAll_Hits(t *testing.T) {
	m, ids := catalogue()
	cases := []struct {
		name               string
		title, description string
		hashtags           []string
		entry, field, term string
	}{
		{"title exact", "Jailer 2 full movie", "", nil, "jailer2", FieldTitle, "Jailer 2"},
		{"case", "JAILER 2 HD", "", nil, "jailer2", FieldTitle, "Jailer 2"},
		{"separator", "my jailer-2 reaction", "", nil, "jailer2", FieldTitle, "Jailer 2"},
		{"one word", "jailer2 leaked", "", nil, "jailer2", FieldTitle, "Jailer 2"},
		{"alias", "Jailer II 1080p", "", nil, "jailer2", FieldTitle, "Jailer II"},
		{"description", "Weekend vlog", "watching Jailer 2 tonight", nil, "jailer2", FieldDescription, "Jailer 2"},
		{"hashtag one word", "Weekend", "", []string{"#jailer2"}, "jailer2", FieldHashtag, "Jailer 2"},
		{"hashtag separated", "Weekend", "", []string{"jailer_2"}, "jailer2", FieldHashtag, "Jailer 2"},
		{"accent", "Pokemon the movie", "", nil, "pokemon", FieldTitle, "Pokémon"},
		{"accent in text", "POKÉMON", "", nil, "pokemon", FieldTitle, "Pokémon"},
		{"apostrophe dropped", "oceans eleven", "", nil, "oceans", FieldTitle, "Ocean's Eleven"},
		{"dotted acronym", "KGF chapter 2", "", nil, "kgf", FieldTitle, "K.G.F Chapter 2"},
		{"spaced acronym", "K G F 2 trailer", "", nil, "kgf", FieldTitle, "KGF 2"},
		{"tamil", "விக்ரம் full movie", "", nil, "tamil", FieldTitle, "விக்ரம்"},
		{"digits title", "96 movie scenes", "", nil, "ninetysix", FieldTitle, "96"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := m.MatchAll(c.title, c.description, c.hashtags)
			if len(got) != 1 {
				t.Fatalf("matches = %+v, want exactly one", got)
			}
			if got[0].EntryID != ids[c.entry] || got[0].Field != c.field || got[0].Term != c.term {
				t.Fatalf("match = %+v, want entry %s field %s term %q", got[0], c.entry, c.field, c.term)
			}
		})
	}
}

func TestMatchAll_NoFalseHits(t *testing.T) {
	m, _ := catalogue()
	for name, c := range map[string]struct {
		title, description string
		hashtags           []string
	}{
		"substring prefix":   {"jailerbird 2", "", nil},
		"substring hashtag":  {"vlog", "", []string{"#jailerbird"}},
		"partial phrase":     {"Jailer the original", "", nil},
		"words apart":        {"jailer and 2 friends", "", nil},
		"reordered":          {"2 jailer", "", nil},
		"digits inside word": {"route966 drive", "", nil},
		"empty":              {"", "", nil},
		"pokemon substring":  {"pokemongo tips", "", nil},
		"later word prefix":  {"Jailer 2nd look", "", nil},
	} {
		if got := m.MatchAll(c.title, c.description, c.hashtags); len(got) != 0 {
			t.Errorf("%s: unexpected matches %+v", name, got)
		}
	}
}

func TestMatchAll_OnePerEntryInFieldOrder(t *testing.T) {
	m, ids := catalogue()
	got := m.MatchAll("Pokemon", "Jailer 2 and Pokémon", []string{"jailer2"})
	if len(got) != 2 {
		t.Fatalf("matches = %+v, want two entries", got)
	}
	if got[0].EntryID != ids["pokemon"] || got[0].Field != FieldTitle {
		t.Fatalf("first = %+v, want pokemon in the title", got[0])
	}
	if got[1].EntryID != ids["jailer2"] || got[1].Field != FieldDescription {
		t.Fatalf("second = %+v, want jailer 2 in the description", got[1])
	}
}

func TestValidTerm(t *testing.T) {
	for term, want := range map[string]bool{
		"Jailer": true, "96": true, "M": false, "  ": false, "!!": false, "#": false, "K.G.F": true,
	} {
		if got := ValidTerm(term); got != want {
			t.Errorf("ValidTerm(%q) = %v, want %v", term, got, want)
		}
	}
	// A blank alias is skipped, not a phrase that matches everything.
	m := New([]Entry{{ID: uuid.New(), Title: "x", Aliases: []string{""}}})
	if m.Len() != 0 || len(m.MatchAll("x marks the spot", "", nil)) != 0 {
		t.Fatal("an unsearchable term was indexed")
	}
}
