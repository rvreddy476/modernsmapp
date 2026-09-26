package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

/*
Structural guards for anonymity.

  - An anonymous post's attachments are scoped at media-service BEFORE the
    row is inserted, and a failure refuses the post (fail closed).
  - A reveal writes its audit row before the identity leaves the function.
  - Only the creator, admins and moderators pass canModerate.
*/

func TestAnonymousAttachmentsAreScopedBeforeTheInsert(t *testing.T) {
	body := codeOnly(funcSource(t, "cross_post.go", "createOnePost"))
	a := strings.Index(body, "s.anonymizeAttachments(")
	i := strings.Index(body, "s.store.CreateGroupPostV2(")
	if a < 0 {
		t.Fatal("createOne never scopes attachments — an anonymous post's picture names its uploader")
	}
	if i < 0 || a > i {
		t.Fatalf("attachments must be scoped before the row is written (anonymize at %d, insert at %d)", a, i)
	}
	if !strings.Contains(body[a:i], "return \"\", nil, err") {
		t.Fatal("a scoping failure does not refuse the post")
	}
}

func TestRevealAuditsBeforeAnswering(t *testing.T) {
	body := codeOnly(funcSource(t, "anonymous_reveal.go", "RevealPostAuthor"))
	audit := strings.Index(body, "s.store.RecordAuthorReveal(")
	ret := strings.LastIndex(body, "return out, nil")
	if audit < 0 || ret < 0 || audit > ret {
		t.Fatalf("the audit row must be written before the anonymous author is returned (audit=%d, return=%d)", audit, ret)
	}
	if !strings.Contains(body, "s.canModerate(") {
		t.Fatal("RevealPostAuthor does not check the caller's role")
	}
}

func TestCanModerateRoles(t *testing.T) {
	body := codeOnly(funcSource(t, "anonymous_reveal.go", "canModerate"))
	for _, must := range []string{"creatorID == actorID", `m.Role == "admin"`, `m.Role == "moderator"`, `m.Status != "active"`} {
		if !strings.Contains(body, must) {
			t.Errorf("canModerate lost %q", must)
		}
	}
	if strings.Contains(body, `m.Role == "member"`) {
		t.Fatal("a plain member must never pass canModerate")
	}
}

func TestAttachmentIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	cases := []struct {
		raw   string
		want  int
		isErr bool
	}{
		{``, 0, false},
		{`null`, 0, false},
		{`[]`, 0, false},
		{`["` + a.String() + `","` + b.String() + `"]`, 2, false},
		{`[{"media_id":"` + a.String() + `","kind":"image"}]`, 1, false},
		{`["not-a-uuid"]`, 0, true},
		{`{"media_id":"` + a.String() + `"}`, 0, true},
	}
	for _, c := range cases {
		ids, err := attachmentIDs(json.RawMessage(c.raw))
		if (err != nil) != c.isErr || len(ids) != c.want {
			t.Errorf("%s: ids=%d err=%v, want %d err=%v", c.raw, len(ids), err, c.want, c.isErr)
		}
	}
}

// anonymizeAttachments calls media-service's internal route for every
// attachment with the internal key, and fails closed on any non-200.
func TestAnonymizeAttachmentsCallsMediaServiceAndFailsClosed(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	var paths []string
	var keys []string
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		keys = append(keys, r.Header.Get("X-Internal-Service-Key"))
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"anonymous"}}`))
	}))
	defer srv.Close()

	s := &Service{internalServiceKey: "internal"}
	s.SetMediaServiceURL(srv.URL)
	raw := json.RawMessage(`["` + a.String() + `","` + b.String() + `"]`)
	if err := s.anonymizeAttachments(t.Context(), raw); err != nil {
		t.Fatalf("scoping two assets: %v", err)
	}
	if len(paths) != 2 || paths[0] != "POST /v1/media/internal/"+a.String()+"/anonymize" || keys[0] != "internal" {
		t.Fatalf("calls: %v keys: %v", paths, keys)
	}

	fail = true
	if err := s.anonymizeAttachments(t.Context(), raw); err == nil {
		t.Fatal("a failing media-service did not refuse the post")
	}

	unconfigured := &Service{}
	if err := unconfigured.anonymizeAttachments(t.Context(), raw); err == nil {
		t.Fatal("no media-service configured must refuse, not silently skip")
	}
	if err := unconfigured.anonymizeAttachments(t.Context(), json.RawMessage(`[]`)); err != nil {
		t.Fatalf("no attachments needs no media-service: %v", err)
	}
}
