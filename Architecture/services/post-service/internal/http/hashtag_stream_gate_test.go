package http

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

type recordingStreamGate struct {
	allow  bool
	called int
	viewer *uuid.UUID
	post   uuid.UUID
	author uuid.UUID
}

func (g *recordingStreamGate) HashtagStreamAllows(_ context.Context, viewerID *uuid.UUID, postID, authorID uuid.UUID) bool {
	g.called++
	g.viewer, g.post, g.author = viewerID, postID, authorID
	return g.allow
}

func publishedEnvelope(t *testing.T, postID, authorID string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "new_post",
		"payload": map[string]any{
			"post_id": postID, "author_id": authorID, "content_type": "flick", "snippet": "hi",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The stream hands the published post and author, and the connected viewer,
// to the service gate, and forwards only on its yes.
func TestAllowHashtagStreamEvent_AsksTheGateForThisViewer(t *testing.T) {
	viewer, post, author := uuid.New(), uuid.New(), uuid.New()
	payload := publishedEnvelope(t, post.String(), author.String())

	yes := &recordingStreamGate{allow: true}
	if !allowHashtagStreamEvent(context.Background(), yes, &viewer, payload) {
		t.Fatal("allowed event dropped")
	}
	if yes.viewer == nil || *yes.viewer != viewer || yes.post != post || yes.author != author {
		t.Fatalf("gate asked about %v/%s/%s", yes.viewer, yes.post, yes.author)
	}

	no := &recordingStreamGate{allow: false}
	if allowHashtagStreamEvent(context.Background(), no, nil, payload) {
		t.Fatal("denied event forwarded")
	}
	if no.viewer != nil {
		t.Fatal("signed-out viewer was not passed as the stranger (nil)")
	}
}

// Undecidable envelopes never reach the client.
func TestAllowHashtagStreamEvent_FailsClosed(t *testing.T) {
	good := publishedEnvelope(t, uuid.NewString(), uuid.NewString())
	if allowHashtagStreamEvent(context.Background(), nil, nil, good) {
		t.Fatal("forwarded with no gate")
	}
	gate := &recordingStreamGate{allow: true}
	for name, payload := range map[string]string{
		"not json":       "{",
		"no post id":     publishedEnvelope(t, "", uuid.NewString()),
		"bad author id":  publishedEnvelope(t, uuid.NewString(), "nope"),
		"missing fields": `{"type":"new_post"}`,
	} {
		if allowHashtagStreamEvent(context.Background(), gate, nil, payload) {
			t.Errorf("%s: forwarded", name)
		}
	}
	if gate.called != 0 {
		t.Fatalf("gate consulted %d times for malformed envelopes", gate.called)
	}
}
