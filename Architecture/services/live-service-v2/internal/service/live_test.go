package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// fakeLiveKit is a no-op LiveKit client. We only care about the token
// strings being non-empty for IssueViewerToken assertions; no other
// behavior is exercised here.
type fakeLiveKit struct{}

func (fakeLiveKit) CreateRoom(_ context.Context, _ string) error { return nil }
func (fakeLiveKit) IssuePublisherToken(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	return "pub-token", nil
}
func (fakeLiveKit) IssueViewerToken(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	return "viewer-token", nil
}
func (fakeLiveKit) StartEgressToS3(_ context.Context, _, _ string) (string, error) {
	return "egress-id", nil
}
func (fakeLiveKit) StopEgress(_ context.Context, _ string) error { return nil }
func (fakeLiveKit) ServerURL() string                            { return "ws://test" }

// fakeGraph implements service.GraphClient. The test sets `follows` per
// (viewer, creator) and reports it back.
type fakeGraph struct {
	follows map[string]bool
	calls   int
}

func (g *fakeGraph) IsFollowing(_ context.Context, viewerID, creatorID uuid.UUID) (bool, error) {
	g.calls++
	return g.follows[viewerID.String()+":"+creatorID.String()], nil
}

// Compile-time guards.
var _ livekit.Client = fakeLiveKit{}
var _ GraphClient = (*fakeGraph)(nil)

// TestIssueViewerToken_PublicStream — public visibility issues a token
// regardless of follow relationship.
func TestIssueViewerToken_PublicStream(t *testing.T) {
	svc := newTestService(&fakeGraph{})
	stream := &postgres.LiveStream{
		ID:            uuid.New(),
		CreatorUserID: uuid.New(),
		LiveKitRoom:   "stream_x",
		Visibility:    visibilityPublic,
		Status:        "live",
	}
	if err := svc.authorizeViewer(context.Background(), stream, uuid.New()); err != nil {
		t.Fatalf("expected public stream to authorize any viewer, got %v", err)
	}
}

// TestIssueViewerToken_FollowersOnly_NonFollower — denied.
func TestIssueViewerToken_FollowersOnly_NonFollower(t *testing.T) {
	creatorID := uuid.New()
	viewerID := uuid.New()
	stream := &postgres.LiveStream{
		ID:            uuid.New(),
		CreatorUserID: creatorID,
		LiveKitRoom:   "stream_y",
		Visibility:    visibilityFollowers,
		Status:        "live",
	}
	graph := &fakeGraph{follows: map[string]bool{}}
	svc := newTestService(graph)
	err := svc.authorizeViewer(context.Background(), stream, viewerID)
	if !errors.Is(err, ErrNotFollower) {
		t.Fatalf("expected ErrNotFollower for non-follower, got %v", err)
	}
}

// TestIssueViewerToken_FollowersOnly_Follower — allowed.
func TestIssueViewerToken_FollowersOnly_Follower(t *testing.T) {
	creatorID := uuid.New()
	viewerID := uuid.New()
	stream := &postgres.LiveStream{
		ID:            uuid.New(),
		CreatorUserID: creatorID,
		LiveKitRoom:   "stream_z",
		Visibility:    visibilityFollowers,
		Status:        "live",
	}
	graph := &fakeGraph{follows: map[string]bool{
		viewerID.String() + ":" + creatorID.String(): true,
	}}
	svc := newTestService(graph)
	if err := svc.authorizeViewer(context.Background(), stream, viewerID); err != nil {
		t.Fatalf("expected follower to be allowed, got %v", err)
	}
}

// TestIssueViewerToken_PaidStream — paid is stubbed and must return the
// paid sentinel so the HTTP layer can map to 402.
func TestIssueViewerToken_PaidStream(t *testing.T) {
	svc := newTestService(&fakeGraph{})
	stream := &postgres.LiveStream{
		ID:            uuid.New(),
		CreatorUserID: uuid.New(),
		LiveKitRoom:   "stream_paid",
		Visibility:    visibilityPaid,
		Status:        "live",
	}
	if err := svc.authorizeViewer(context.Background(), stream, uuid.New()); !errors.Is(err, ErrPaidNotSupported) {
		t.Fatalf("expected ErrPaidNotSupported, got %v", err)
	}
}

// TestNormalizeVisibility covers the input sanitiser. Anything outside
// public/followers/paid (after trim + lower) must return "" so
// CreateStream rejects it with ErrInvalidVisibility.
func TestNormalizeVisibility(t *testing.T) {
	cases := map[string]string{
		"":            visibilityPublic,
		" public ":    visibilityPublic,
		"FOLLOWERS":   visibilityFollowers,
		"paid":        visibilityPaid,
		"private":     "",
		"weird-value": "",
	}
	for in, want := range cases {
		if got := normalizeVisibility(in); got != want {
			t.Errorf("normalizeVisibility(%q): got %q, want %q", in, got, want)
		}
	}
}

// newTestService wires a Service that uses a fake LiveKit client, a
// nil store/producer/redis (paths we don't exercise here), and the
// supplied graph fake. Service does not touch nil store/producer for
// the authorizeViewer code path.
func newTestService(graph GraphClient) *Service {
	return &Service{
		store:    nil, // not used by authorizeViewer
		livekit:  fakeLiveKit{},
		graph:    graph,
		producer: nil,
		redis:    nil,
	}
}
