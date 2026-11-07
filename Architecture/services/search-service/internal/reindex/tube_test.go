package reindex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/postclient"
	"github.com/atpost/search-service/internal/store/search"
)

// The tube reindex's visibility rule: a public playlist is indexed, any
// other visibility is DELETED from the index. post-service lists every
// playlist and says which are public; the index-time decision is made
// here, and this is the test that keeps it honest.

// Production ids are Postgres UUIDs; the bulk writer refuses anything else.
const (
	chanID = "c1c1c1c1-0000-4000-8000-000000000001"
	pubID  = "b0b0b0b0-0000-4000-8000-000000000002"
	privID = "a0a0a0a0-0000-4000-8000-000000000003"
	unlID  = "d0d0d0d0-0000-4000-8000-000000000004"
)

// fakeOS records what the reindex wrote to OpenSearch.
type fakeOS struct {
	mu       sync.Mutex
	indexed  map[string][]string // index -> ids written through _bulk
	deleted  map[string][]string // index -> ids deleted
	bulkDocs map[string]map[string]any
	sweeps   map[string][]map[string]any // index -> _delete_by_query bodies
	failBulk bool
	srv      *httptest.Server
}

func newFakeOS(t *testing.T) (*search.Store, *fakeOS) {
	t.Helper()
	f := &fakeOS{indexed: map[string][]string{}, deleted: map[string][]string{}, bulkDocs: map[string]map[string]any{}, sweeps: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_bulk"):
			body, _ := io.ReadAll(r.Body)
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			items := []map[string]any{}
			for i := 0; i+1 < len(lines); i += 2 {
				// An `index` action carries the document; an `update` (the
				// keep-hidden upsert) carries it in script.params.doc.
				var meta map[string]struct {
					Index string `json:"_index"`
					ID    string `json:"_id"`
				}
				_ = json.Unmarshal([]byte(lines[i]), &meta)
				var src struct {
					Script struct {
						Params struct {
							Doc map[string]any `json:"doc"`
						} `json:"params"`
					} `json:"script"`
				}
				var doc map[string]any
				_ = json.Unmarshal([]byte(lines[i+1]), &doc)
				_ = json.Unmarshal([]byte(lines[i+1]), &src)
				for op, m := range meta {
					if op == "update" {
						doc = src.Script.Params.Doc
					}
					status := 201
					if f.failBulk {
						status = 503
					} else {
						f.indexed[m.Index] = append(f.indexed[m.Index], m.ID)
						f.bulkDocs[m.ID] = doc
					}
					items = append(items, map[string]any{op: map[string]any{"_id": m.ID, "status": status}})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": f.failBulk, "items": items})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_delete_by_query"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			index := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
			f.sweeps[index] = append(f.sweeps[index], body)
			fmt.Fprint(w, `{"deleted":1}`)
		case r.Method == http.MethodDelete && strings.Count(r.URL.Path, "/") == 3:
			// /<index>/_doc/<id>
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			f.deleted[parts[0]] = append(f.deleted[parts[0]], parts[2])
			fmt.Fprint(w, `{"result":"deleted"}`)
		case strings.HasSuffix(r.URL.Path, "/_count"):
			fmt.Fprint(w, `{"count":0}`)
		default:
			fmt.Fprint(w, `{"acknowledged":true}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	store, err := search.New(f.srv.URL)
	if err != nil {
		t.Fatalf("search.New: %v", err)
	}
	return store, f
}

// fakePostService serves the two internal listings.
func fakePostService(t *testing.T, channels []map[string]any, playlists []map[string]any) *postclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Internal-Service-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var items []map[string]any
		switch r.URL.Path {
		case "/internal/tube/channels":
			items = channels
		case "/internal/tube/playlists":
			items = playlists
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("after") != "" {
			items = nil // one page is all there is
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"items": items, "next_after": "", "has_more": false,
		}})
	}))
	t.Cleanup(srv.Close)
	return postclient.New(srv.URL, "test-key")
}

func TestReindexTube_IndexesPublicPlaylistsAndDeletesTheRest(t *testing.T) {
	store, os := newFakeOS(t)
	now := time.Now().UTC().Format(time.RFC3339)
	owner := "11111111-1111-1111-1111-111111111111"
	client := fakePostService(t,
		[]map[string]any{
			{"id": chanID, "owner_id": owner, "name": "Cooking with Asha", "handle": "asha", "about": "weeknight food",
				"avatar_media_id": "m-avatar", "follower_count": 42, "created_at": now, "updated_at": now},
		},
		[]map[string]any{
			{"id": pubID, "owner_id": owner, "title": "Best mixes", "description": "", "visibility": "public",
				"item_count": 7, "cover_media_id": nil, "cover_url": nil, "created_at": now, "updated_at": now},
			// post-service discloses a non-public playlist by id and
			// visibility only. The rows here carry MORE than that on
			// purpose: the reindex must delete on visibility alone, not
			// because the title happened to be missing.
			{"id": privID, "owner_id": owner, "title": "Secret", "visibility": "private", "item_count": 1, "created_at": now, "updated_at": now},
			{"id": unlID, "owner_id": owner, "title": "Link only", "visibility": "unlisted", "item_count": 2, "created_at": now, "updated_at": now},
		},
	)

	res, err := ReindexTube(context.Background(), client, store, slog.Default())
	if err != nil {
		t.Fatalf("ReindexTube: %v", err)
	}
	if res.ChannelsIndexed != 1 || res.CollectionsIndexed != 1 || res.RemovedNonPublic != 2 {
		t.Fatalf("result = %+v, want 1 channel, 1 collection, 2 removed", res)
	}
	if got := os.indexed[search.IndexTubeChannels]; len(got) != 1 || got[0] != chanID {
		t.Fatalf("channels written = %v, want [%s]", got, chanID)
	}
	if got := os.indexed[search.IndexTubeCollections]; len(got) != 1 || got[0] != pubID {
		t.Fatalf("collections written = %v, want only the public playlist; a private title in the index is a leak", got)
	}
	if got := os.deleted[search.IndexTubeCollections]; len(got) != 2 || got[0] != privID || got[1] != unlID {
		t.Fatalf("collections deleted = %v, want [%s %s]", got, privID, unlID)
	}
	doc := os.bulkDocs[chanID]
	if doc["name"] != "Cooking with Asha" || doc["handle"] != "asha" || doc["owner_id"] != owner || doc["follower_count"].(float64) != 42 || doc["avatar_media_id"] != "m-avatar" {
		t.Fatalf("channel document = %v", doc)
	}
	if pub := os.bulkDocs[pubID]; pub["visibility"] != "public" || pub["item_count"].(float64) != 7 {
		t.Fatalf("collection document = %v", pub)
	}
}

// The store refuses a non-public document outright, so even a caller that
// forgets the rule cannot write one.
func TestBulkIndexTubeCollections_RefusesNonPublic(t *testing.T) {
	store, os := newFakeOS(t)
	_, err := store.BulkIndexTubeCollections(context.Background(), []search.TubeCollectionDoc{
		{PlaylistID: "x", OwnerID: "o", Title: "secret", Visibility: "private"},
	})
	if err == nil {
		t.Fatal("a private playlist document was accepted for indexing")
	}
	if len(os.indexed[search.IndexTubeCollections]) != 0 {
		t.Fatalf("written anyway: %v", os.indexed)
	}
}

func TestTubeCollectionDocOf(t *testing.T) {
	owner := "o"
	if _, ok := TubeCollectionDocOf(postclient.TubePlaylist{ID: "p", Visibility: "unlisted", OwnerID: &owner}); ok {
		t.Fatal("unlisted is not discoverable")
	}
	if _, ok := TubeCollectionDocOf(postclient.TubePlaylist{ID: "p", Visibility: "public"}); ok {
		t.Fatal("a public row without an owner is malformed, not indexable")
	}
	doc, ok := TubeCollectionDocOf(postclient.TubePlaylist{ID: "p", Visibility: "public", OwnerID: &owner, Title: "t", ItemCount: 3})
	if !ok || doc.PlaylistID != "p" || doc.OwnerID != "o" || doc.Title != "t" || doc.ItemCount != 3 {
		t.Fatalf("doc = %+v ok=%v", doc, ok)
	}
}

// S-M3: post-service hard-deletes playlists and publishes nothing, so a
// complete walk stamps every document it writes and then sweeps the ones it
// did not: older than the run's start, or never stamped.
func TestReindexTube_SweepsWhatAFullWalkNoLongerLists(t *testing.T) {
	store, os := newFakeOS(t)
	now := time.Now().UTC().Format(time.RFC3339)
	owner := "11111111-1111-1111-1111-111111111111"
	client := fakePostService(t,
		[]map[string]any{{"id": chanID, "owner_id": owner, "name": "n", "handle": "h", "created_at": now, "updated_at": now}},
		[]map[string]any{{"id": pubID, "owner_id": owner, "title": "t", "visibility": "public", "created_at": now, "updated_at": now}},
	)
	before := time.Now().UTC()
	res, err := ReindexTube(context.Background(), client, store, slog.Default())
	if err != nil {
		t.Fatalf("ReindexTube: %v", err)
	}
	if res.SweepSkipped != "" || res.ChannelsSwept != 1 || res.CollectionsSwept != 1 {
		t.Fatalf("result = %+v, want both indices swept", res)
	}
	for _, index := range []string{search.IndexTubeChannels, search.IndexTubeCollections} {
		sweeps := os.sweeps[index]
		if len(sweeps) != 1 {
			t.Fatalf("%s: %d sweeps, want 1", index, len(sweeps))
		}
		cut := sweepCutoff(t, sweeps[0])
		if cut.Before(before.Add(-time.Second)) || cut.After(time.Now().UTC()) {
			t.Fatalf("%s: sweep cut-off %v is not this run's start", index, cut)
		}
	}
	for _, id := range []string{chanID, pubID} {
		stamp, _ := os.bulkDocs[id]["reindexed_at"].(string)
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || at.Before(before.Add(-time.Second)) {
			t.Fatalf("document %s carries reindexed_at %q; an unstamped document is swept by the next run", id, stamp)
		}
	}
}

// Only a walk that wrote everything may sweep, and an empty listing never
// sweeps: a listing outage must not empty the index.
func TestReindexTube_NoSweepAfterAFailedWriteOrAnEmptyListing(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	owner := "11111111-1111-1111-1111-111111111111"

	store, os := newFakeOS(t)
	os.failBulk = true
	client := fakePostService(t,
		[]map[string]any{{"id": chanID, "owner_id": owner, "name": "n", "handle": "h", "created_at": now, "updated_at": now}},
		[]map[string]any{{"id": pubID, "owner_id": owner, "title": "t", "visibility": "public", "created_at": now, "updated_at": now}},
	)
	res, err := ReindexTube(context.Background(), client, store, slog.Default())
	if err != nil {
		t.Fatalf("ReindexTube: %v", err)
	}
	if len(os.sweeps) != 0 || res.SweepSkipped == "" {
		t.Fatalf("swept after failed writes: sweeps=%v result=%+v", os.sweeps, res)
	}

	store, os = newFakeOS(t)
	res, err = ReindexTube(context.Background(), fakePostService(t, nil, nil), store, slog.Default())
	if err != nil {
		t.Fatalf("ReindexTube: %v", err)
	}
	if len(os.sweeps) != 0 || res.SweepSkipped == "" {
		t.Fatalf("swept after an empty listing: sweeps=%v result=%+v", os.sweeps, res)
	}
}

func sweepCutoff(t *testing.T, body map[string]any) time.Time {
	t.Helper()
	raw, _ := json.Marshal(body)
	var q struct {
		Query struct {
			Bool struct {
				MustNot []struct {
					Range struct {
						ReindexedAt struct {
							Gte string `json:"gte"`
						} `json:"reindexed_at"`
					} `json:"range"`
				} `json:"must_not"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &q); err != nil || len(q.Query.Bool.MustNot) != 1 {
		t.Fatalf("sweep body %s is not must_not range reindexed_at", raw)
	}
	at, err := time.Parse(time.RFC3339Nano, q.Query.Bool.MustNot[0].Range.ReindexedAt.Gte)
	if err != nil {
		t.Fatalf("sweep cut-off: %v", err)
	}
	return at
}
