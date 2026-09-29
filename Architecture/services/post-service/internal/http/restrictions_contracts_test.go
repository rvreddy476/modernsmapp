package http

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/moderationcap"
	"github.com/google/uuid"
)

// Golden JSON contracts for the restriction protocol (Copyright Match
// plan, section 6.2) under testdata/contracts/restrictions/. Same rule as
// the MTube fixtures: each response file is the `data` member of the
// api.JSON envelope marshalled from the very structs the handlers return,
// so a struct change that does not update the fixture fails here.
//
//	UPDATE_CONTRACTS=1 go test ./internal/http -run TestRestrictionContracts
//
// The request fixture is what trust-safety must send (claims + detached
// capability); the signature in it is illustrative, minted with a test key.

var (
	fxCase       = uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	fxDecision   = uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	fxActor      = uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	fxRestrictID = uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	fxBaseDec    = uuid.MustParse("abababab-abab-4bab-8bab-abababababab")
)

func fixtureRestrictionClaims() moderationcap.RestrictionClaims {
	return moderationcap.RestrictionClaims{
		Version: 1, Issuer: moderationcap.IssuerTrustSafety, Purpose: moderationcap.PurposePostRestriction, Audience: moderationcap.AudiencePostService,
		Action: "place_hold", Source: "copyright", CaseID: fxCase.String(), CaseRevision: 7,
		SubjectID: fxPost.String(), SubjectAuthorID: fxAuthor.String(), ExpectedState: "absent",
		DecisionID: fxDecision.String(), PolicyVersion: "copyright-v1", ReasonCode: "removal_upheld", ActorID: fxActor.String(),
		IssuedAtUnix: fxTime.Unix(), ExpiresAtUnix: fxTime.Add(5 * time.Minute).Unix(),
	}
}

func fixtureRestriction() postgres.PostRestriction {
	return postgres.PostRestriction{
		RestrictionID: fxRestrictID, PostID: fxPost, Source: "copyright", CaseID: fxCase, Scope: "global", State: "active",
		CaseRevision: 7, LastDecisionID: fxDecision, PolicyVersion: "copyright-v1", ReasonCode: "removal_upheld",
		FirstPlacedAt: fxTime, PlacedAt: fxTime, ReleasedAt: nil, UpdatedAt: fxTime,
	}
}

func restrictionContracts() map[string]any {
	claims := fixtureRestrictionClaims()
	outcome := postgres.RestrictionOutcome{
		RestrictionID: fxRestrictID, PostID: fxPost, CaseID: fxCase, Source: "copyright", State: "active", CaseRevision: 7,
		ActiveRestrictionCount: 1, EffectiveReviewStatus: "restricted", Changed: true, Replayed: false,
	}
	replay := outcome
	replay.Replayed = true
	release := claims
	release.Action, release.ReasonCode, release.ExpectedState, release.CaseRevision = "release_hold", "claim_withdrawn", "active", 8
	release.DecisionID = uuid.MustParse("dddddddd-dddd-4ddd-8ddd-000000000002").String()
	released := fixtureRestriction()
	released.State, released.CaseRevision, released.LastDecisionID, released.ReasonCode = "released", 8, uuid.MustParse(release.DecisionID), "claim_withdrawn"
	at := fxTime.Add(time.Hour)
	released.ReleasedAt, released.UpdatedAt = &at, at
	subject := postgres.ModerationSubject{
		PostID: fxPost, AuthorID: fxAuthor, ReviewStatus: "approved", SearchRev: 4, Deleted: false,
		BaseReviewStatus: "approved", EffectiveReviewStatus: "restricted", LatestBaseDecisionID: &fxBaseDec,
		LastDecisionID: &fxBaseDec, LastDecisionSource: postgres.LastDecisionSourceCopyright, LatestBaseDecisionSource: postgres.LastDecisionSourceCopyright,
		ActiveRestrictions: []postgres.PostRestriction{fixtureRestriction()},
	}
	upload := service.UploadDetail{
		PostDetail:       service.PostDetail{Post: fixturePost()},
		ScheduledAt:      nil,
		ProcessingStatus: "ready",
		Flags:            []string{service.UploadFlagCopyrightHold},
		Restrictions:     []service.RestrictionNotice{{CaseID: fxCase, Source: "copyright", ReasonCode: "removal_upheld", PolicyVersion: "copyright-v1", PlacedAt: fxTime}},
		Description:      "0:00 Intro\n1:23 Setup\n12:05 The build",
	}
	return map[string]any{
		"command_place_request.json":     map[string]any{"claims": claims, "capability": "base64url-HMAC-SHA256-of-the-canonical-claims-under-POST_RESTRICTION_HMAC_KEY"},
		"command_release_request.json":   map[string]any{"claims": release, "capability": "base64url-HMAC-SHA256-of-the-canonical-claims-under-POST_RESTRICTION_HMAC_KEY"},
		"command_response.json":          outcome,
		"command_replay_response.json":   replay,
		"list_response.json":             restrictionListResponse{Items: []postgres.PostRestriction{fixtureRestriction(), released}, NextCursor: at.UTC().Format(time.RFC3339Nano) + "_" + fxRestrictID.String()},
		"moderation_subject.json":        subject,
		"hub_upload_row_restricted.json": upload,
	}
}

func TestRestrictionContracts(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "restrictions")
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range restrictionContracts() {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join(dir, name)
			if update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("fixture %s is stale.\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		})
	}
}

// The request fixtures decode into the handler's request struct and pass
// the protocol's own validation, so the documented shape is one the
// server accepts (given a real signature).
func TestRestrictionRequestFixturesDecodeAndValidate(t *testing.T) {
	dir := filepath.Join("testdata", "contracts", "restrictions")
	for _, name := range []string{"command_place_request.json", "command_release_request.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var req restrictionCommandRequest
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := req.Claims.Validate(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if req.Capability == "" {
			t.Fatalf("%s: capability missing", name)
		}
	}
}
