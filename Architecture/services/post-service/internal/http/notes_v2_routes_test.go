package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Echo v2 routes (notes_v2_handler.go) through the real router, and the v2
// golden fixtures (docs/designs/echo-v2-contract.md §3): note_v2.json,
// note_scheduled.json, trends.json, error_poll_closed.json.

var (
	ntPoll       = uuid.MustParse("f5555555-5555-4555-8555-555555555555")
	ntQuote2     = uuid.MustParse("f6666666-6666-4666-8666-666666666666")
	ntCarolNote  = uuid.MustParse("f7777777-7777-4777-8777-777777777777")
	ntScheduled  = uuid.MustParse("f8888888-8888-4888-8888-888888888888")
	ntClosedPoll = uuid.MustParse("f9999999-9999-4999-8999-999999999999")
)

// seedV2 adds Alice's poll note (Bob and Carol voted, Bob bookmarked it,
// Bob quoted it), Carol's note, Alice's scheduled note and a closed poll.
func (nr *noteRouter) seedV2() {
	at := func(d time.Duration) time.Time { return ntTime.Add(d) }
	nr.store.Put(postgres.Note{
		Post: postgres.Post{ID: ntPoll, AuthorID: ntAlice, Text: "Best monsoon snack? #Bengaluru #monsoon",
			Hashtags: []string{"bengaluru", "monsoon"}, PostType: "text", AppOrigin: "echo", CreatedAt: at(5 * time.Minute), UpdatedAt: at(5 * time.Minute)},
		NoteFields: postgres.NoteFields{ViewCount: 7},
	})
	ends := ntNow.Add(24 * time.Hour)
	poll := &postgres.PollData{EndsAt: &ends, Options: []postgres.PollOption{{Label: "Bajji"}, {Label: "Samosa"}}}
	nr.store.PutPoll(ntPoll, poll)
	nr.store.Votes[[2]uuid.UUID{ntPoll, ntBob}] = []uuid.UUID{poll.Options[0].ID}
	nr.store.Votes[[2]uuid.UUID{ntPoll, ntCarol}] = []uuid.UUID{poll.Options[1].ID}
	nr.store.Bookmarks[[2]uuid.UUID{ntBob, ntPoll}] = ntNow
	quoteOf := ntPoll
	nr.store.Put(postgres.Note{
		Post: postgres.Post{ID: ntQuote2, AuthorID: ntBob, Text: "Bajji, obviously #Bengaluru", Hashtags: []string{"bengaluru"},
			PostType: "text", AppOrigin: "echo", CreatedAt: at(6 * time.Minute), UpdatedAt: at(6 * time.Minute)},
		NoteFields: postgres.NoteFields{QuoteOfID: &quoteOf},
	})
	nr.store.Put(postgres.Note{
		Post: postgres.Post{ID: ntCarolNote, AuthorID: ntCarol, Text: "Flooded again #bengaluru #monsoon", Hashtags: []string{"bengaluru", "monsoon"},
			PostType: "text", AppOrigin: "echo", CreatedAt: at(7 * time.Minute), UpdatedAt: at(7 * time.Minute)},
	})
	publishAt := ntNow.Add(3 * time.Hour)
	nr.store.Put(postgres.Note{
		Post: postgres.Post{ID: ntScheduled, AuthorID: ntAlice, Text: "Weekend plans, posted later #weekend", Hashtags: []string{"weekend"},
			PostType: "text", AppOrigin: "echo", CreatedAt: ntNow, UpdatedAt: ntNow, PublishAt: &publishAt, IsScheduled: true},
	})
	closed := ntNow.Add(-time.Minute)
	nr.store.Put(postgres.Note{
		Post: postgres.Post{ID: ntClosedPoll, AuthorID: ntAlice, Text: "Closed already?", PostType: "text", AppOrigin: "echo",
			CreatedAt: at(-time.Hour), UpdatedAt: at(-time.Hour)},
	})
	nr.store.PutPoll(ntClosedPoll, &postgres.PollData{EndsAt: &closed, Options: []postgres.PollOption{{Label: "Yes"}, {Label: "No"}}})
}

func addNoteV2Contracts(t *testing.T, nr *noteRouter, get func(name, method, path string, user *uuid.UUID, body string, key bool, status int)) {
	t.Helper()
	nr.seedV2()
	get("note_v2.json", "GET", "/v1/notes/"+ntPoll.String(), &ntBob, "", false, 200)
	get("note_scheduled.json", "GET", "/v1/notes/me/scheduled", &ntAlice, "", false, 200)
	get("trends.json", "GET", "/v1/notes/trends", nil, "", false, 200)
	get("error_poll_closed.json", "POST", "/v1/notes/"+ntClosedPoll.String()+"/poll/vote", &ntBob, `{"option_index":0}`, false, 409)
}

var noteV2Routes = []struct{ method, path string }{
	{http.MethodPost, "/v1/notes/" + ntPoll.String() + "/poll/vote"},
	{http.MethodGet, "/v1/notes/me/scheduled"},
	{http.MethodPatch, "/v1/notes/" + ntScheduled.String() + "/schedule"},
	{http.MethodDelete, "/v1/notes/" + ntScheduled.String() + "/schedule"},
	{http.MethodPost, "/v1/notes/" + ntPoll.String() + "/bookmark"},
	{http.MethodDelete, "/v1/notes/" + ntPoll.String() + "/bookmark"},
	{http.MethodGet, "/v1/notes/bookmarks"},
	{http.MethodPost, "/v1/notes/views"},
	{http.MethodGet, "/v1/notes/" + ntPoll.String() + "/quotes"},
	{http.MethodGet, "/v1/notes/trends"},
	{http.MethodGet, "/v1/notes/hashtags/bengaluru"},
	{http.MethodGet, "/v1/notes/search?q=monsoon"},
}

func TestNoteV2Routes_EchoSwitchedOffIs503Everywhere(t *testing.T) {
	nr := newNoteRouter(t, false)
	nr.seed()
	nr.seedV2()
	for _, rt := range noteV2Routes {
		r := nr.call(t, rt.method, rt.path, &ntAlice, `{}`, false)
		if r.code != http.StatusServiceUnavailable || r.env.Error == nil || r.env.Error.Code != CodeEchoNotEnabled {
			t.Errorf("%s %s with ECHO_ENABLED off: %d %s", rt.method, rt.path, r.code, r.body)
		}
	}
}

func TestNoteV2Routes_Answers(t *testing.T) {
	nr := newNoteRouter(t, true)
	nr.seed()
	nr.seedV2()
	later := ntNow.Add(5 * time.Hour).Format(time.RFC3339)
	ok := []struct {
		method, path string
		user         *uuid.UUID
		body         string
		status       int
	}{
		{"GET", "/v1/notes/me/scheduled", &ntAlice, "", 200},
		{"PATCH", "/v1/notes/" + ntScheduled.String() + "/schedule", &ntAlice, `{"scheduled_at":"` + later + `"}`, 200},
		{"POST", "/v1/notes/" + ntTop.String() + "/bookmark", &ntBob, "", 200},
		{"DELETE", "/v1/notes/" + ntTop.String() + "/bookmark", &ntBob, "", 200},
		{"GET", "/v1/notes/bookmarks", &ntBob, "", 200},
		{"POST", "/v1/notes/views", nil, `{"note_ids":["` + ntTop.String() + `"]}`, 204},
		{"POST", "/v1/notes/views", &ntBob, `{"note_ids":["` + ntTop.String() + `"]}`, 204},
		{"GET", "/v1/notes/" + ntPoll.String() + "/quotes", nil, "", 200},
		{"GET", "/v1/notes/trends?limit=5", nil, "", 200},
		{"GET", "/v1/notes/hashtags/Bengaluru?tab=latest", nil, "", 200},
		{"GET", "/v1/notes/search?q=monsoon&tab=top", nil, "", 200},
		{"POST", "/v1/notes/" + ntPoll.String() + "/poll/vote", &ntAlice, `{"option_index":0}`, 403},
		{"DELETE", "/v1/notes/" + ntScheduled.String() + "/schedule", &ntAlice, "", 204},
	}
	for _, c := range ok {
		r := nr.call(t, c.method, c.path, c.user, c.body, false)
		if r.code != c.status {
			t.Errorf("%s %s: %d %s", c.method, c.path, r.code, r.body)
		}
	}
	if nr.store.Note(ntTop).ViewCount != 1 {
		t.Fatal("the signed-in view was not counted once")
	}
	var b struct {
		Bookmarked bool  `json:"bookmarked"`
		Count      int64 `json:"count"`
	}
	r := nr.call(t, "POST", "/v1/notes/"+ntPoll.String()+"/bookmark", &ntCarol, "", false)
	if err := json.Unmarshal(r.env.Data, &b); err != nil || !b.Bookmarked || b.Count != 2 {
		t.Fatalf("bookmark answer %s", r.body)
	}
}

func TestNoteV2Routes_Refusals(t *testing.T) {
	nr := newNoteRouter(t, true)
	nr.seed()
	nr.seedV2()
	img := uuid.New()
	nr.store.Media[img] = postgres.NoteMediaRow{UploaderID: ntAlice, Kind: "image", MimeType: "image/jpeg", ProcessingStatus: "ready", ModerationStatus: "passed"}
	soon := ntNow.Add(time.Minute).Format(time.RFC3339)
	later := ntNow.Add(time.Hour).Format(time.RFC3339)
	poll, sched := ntPoll.String(), ntScheduled.String()
	many := `{"note_ids":[` + `"` + uuid.NewString() + `"`
	for i := 0; i < service.NoteViewsMaxBatch; i++ {
		many += `,"` + uuid.NewString() + `"`
	}
	many += `]}`
	cases := []struct {
		name, method, path string
		user               *uuid.UUID
		body               string
		key                bool
		status             int
		code               string
	}{
		{"poll one choice", "POST", "/v1/notes", &ntAlice, `{"text":"q","poll":{"options":["a"],"duration_minutes":60}}`, true, 400, CodeInvalidPoll},
		{"poll too short", "POST", "/v1/notes", &ntAlice, `{"text":"q","poll":{"options":["a","b"],"duration_minutes":4}}`, true, 400, CodeInvalidPoll},
		{"poll with media", "POST", "/v1/notes", &ntAlice, `{"text":"q","media_ids":["` + img.String() + `"],"poll":{"options":["a","b"],"duration_minutes":60}}`, true, 400, CodePollWithMedia},
		{"schedule too soon", "POST", "/v1/notes", &ntAlice, `{"text":"q","scheduled_at":"` + soon + `"}`, true, 400, CodeInvalidSchedule},
		{"scheduled reply", "POST", "/v1/notes", &ntBob, `{"text":"q","reply_to_id":"` + ntTop.String() + `","scheduled_at":"` + later + `"}`, true, 400, CodeInvalidSchedule},
		{"vote signed out", "POST", "/v1/notes/" + poll + "/poll/vote", nil, `{"option_index":0}`, false, 401, "UNAUTHORIZED"},
		{"vote twice", "POST", "/v1/notes/" + poll + "/poll/vote", &ntBob, `{"option_index":1}`, false, 409, CodeAlreadyVoted},
		{"own poll", "POST", "/v1/notes/" + poll + "/poll/vote", &ntAlice, `{"option_index":0}`, false, 403, CodeOwnPoll},
		{"closed poll", "POST", "/v1/notes/" + ntClosedPoll.String() + "/poll/vote", &ntBob, `{"option_index":0}`, false, 409, CodePollClosed},
		{"no option", "POST", "/v1/notes/" + poll + "/poll/vote", &ntBob, `{}`, false, 400, CodeInvalidPoll},
		{"vote on a scheduled note", "POST", "/v1/notes/" + sched + "/poll/vote", &ntBob, `{"option_index":0}`, false, 404, CodeNoteNotFound},
		{"get a scheduled note (author)", "GET", "/v1/notes/" + sched, &ntAlice, "", false, 404, CodeNoteNotFound},
		{"replies of a scheduled note", "GET", "/v1/notes/" + sched + "/replies", &ntAlice, "", false, 404, CodeNoteNotFound},
		{"internal batch skips it", "GET", "/internal/notes/batch?ids=" + sched, &ntAlice, "", false, 200, ""},
		{"stranger reschedules", "PATCH", "/v1/notes/" + sched + "/schedule", &ntBob, `{"scheduled_at":"` + later + `"}`, false, 404, CodeNoteNotFound},
		{"reschedule too soon", "PATCH", "/v1/notes/" + sched + "/schedule", &ntAlice, `{"scheduled_at":"` + soon + `"}`, false, 400, CodeInvalidSchedule},
		{"reschedule no time", "PATCH", "/v1/notes/" + sched + "/schedule", &ntAlice, `{}`, false, 400, CodeInvalidSchedule},
		{"stranger deletes schedule", "DELETE", "/v1/notes/" + sched + "/schedule", &ntBob, "", false, 404, CodeNoteNotFound},
		{"scheduled list signed out", "GET", "/v1/notes/me/scheduled", nil, "", false, 401, "UNAUTHORIZED"},
		{"bookmark signed out", "POST", "/v1/notes/" + poll + "/bookmark", nil, "", false, 401, "UNAUTHORIZED"},
		{"bookmarks signed out", "GET", "/v1/notes/bookmarks", nil, "", false, 401, "UNAUTHORIZED"},
		{"bookmark a scheduled note", "POST", "/v1/notes/" + sched + "/bookmark", &ntBob, "", false, 404, CodeNoteNotFound},
		{"51 views", "POST", "/v1/notes/views", &ntBob, many, false, 400, "INVALID_REQUEST"},
		{"bad tab", "GET", "/v1/notes/hashtags/rain?tab=popular", nil, "", false, 400, "INVALID_REQUEST"},
		{"quotes of a missing note", "GET", "/v1/notes/" + uuid.NewString() + "/quotes", nil, "", false, 404, CodeNoteNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := nr.call(t, c.method, c.path, c.user, c.body, c.key)
			r.want(t, c.status, c.code)
			if c.name == "internal batch skips it" && string(r.env.Data) != `{"items":[]}` {
				t.Fatalf("internal batch: %s", r.body)
			}
		})
	}
	if nr.store.Note(ntPoll).ViewCount != 7 {
		t.Fatal("a refused views batch counted")
	}
}

// The fixtures say what the v2 contract says.
func TestNoteV2FixturesMeanWhatTheContractSays(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "contracts", "notes")
	read := func(name string) map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var env map[string]any
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	note := read("note_v2.json")["data"].(map[string]any)
	for _, k := range []string{"view_count", "bookmark_count", "viewer_has_bookmarked", "quote_count", "status", "scheduled_at", "poll"} {
		if _, ok := note[k]; !ok {
			t.Errorf("note_v2.json lacks %q", k)
		}
	}
	if note["status"] != "published" || note["scheduled_at"] != nil || note["view_count"] != float64(7) ||
		note["bookmark_count"] != float64(1) || note["viewer_has_bookmarked"] != true || note["quote_count"] != float64(1) {
		t.Fatalf("note_v2.json counts: %v", note)
	}
	poll := note["poll"].(map[string]any)
	if poll["total_votes"] != float64(2) || poll["viewer_vote"] != float64(0) || poll["closed"] != false {
		t.Fatalf("note_v2.json poll: %v", poll)
	}
	if opts := poll["options"].([]any); len(opts) != 2 || opts[0].(map[string]any)["votes"] != float64(1) {
		t.Fatalf("note_v2.json options: %v", poll["options"])
	}
	for _, name := range []string{"note.json"} {
		n := read(name)["data"].(map[string]any)
		if n["poll"] != nil || n["status"] != "published" {
			t.Fatalf("%s: poll %v status %v", name, n["poll"], n["status"])
		}
	}
	sched := read("note_scheduled.json")["data"].(map[string]any)
	items := sched["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("note_scheduled.json items %v", items)
	}
	s := items[0].(map[string]any)
	if s["status"] != "scheduled" || s["scheduled_at"] == nil {
		t.Fatalf("note_scheduled.json: %v", s)
	}
	trends := read("trends.json")["data"].(map[string]any)["items"].([]any)
	if len(trends) != 1 {
		t.Fatalf("trends.json: %v", trends)
	}
	tr := trends[0].(map[string]any)
	if tr["tag"] != "bengaluru" || tr["rank"] != float64(1) || tr["note_count"] == nil {
		t.Fatalf("trends.json row: %v", tr)
	}
	if e := read("error_poll_closed.json")["error"].(map[string]any); e["code"] != CodePollClosed {
		t.Fatalf("error_poll_closed.json: %v", e)
	}
}
