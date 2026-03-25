package service

// live_test.go — integration-style unit tests using an in-memory fake store.
//
// Because this is package service (not package service_test), we can
// create Service values with a testStore substituted in place of the real
// *postgres.Store so we never need a running database.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// =============================================================================
// In-memory test store
// =============================================================================

// testStore is a minimal in-memory fake that satisfies every method the Service
// calls during tests. Only the paths exercised by the five test cases need real
// implementations.
type testStore struct {
	streams       map[uuid.UUID]*postgres.Stream
	mutes         map[string]bool     // muteKey → true
	wordFilters   map[string][]string // streamID.String() → slice of words
	pollVotes     map[string]bool     // pollVoteKey → true
	polls         map[uuid.UUID]*postgres.LivePoll
	guestStatuses map[string]string // guestKey → status
}

func newTestStore() *testStore {
	return &testStore{
		streams:       make(map[uuid.UUID]*postgres.Stream),
		mutes:         make(map[string]bool),
		wordFilters:   make(map[string][]string),
		pollVotes:     make(map[string]bool),
		polls:         make(map[uuid.UUID]*postgres.LivePoll),
		guestStatuses: make(map[string]string),
	}
}

func (s *testStore) muteKey(streamID, userID uuid.UUID) string {
	return streamID.String() + ":m:" + userID.String()
}
func (s *testStore) pollVoteKey(pollID, userID uuid.UUID) string {
	return pollID.String() + ":v:" + userID.String()
}
func (s *testStore) guestKey(streamID, userID uuid.UUID) string {
	return streamID.String() + ":g:" + userID.String()
}

// --- Streams ---

func (s *testStore) CreateStream(ctx context.Context, st *postgres.Stream) error {
	cp := *st
	s.streams[st.ID] = &cp
	return nil
}

func (s *testStore) GetStream(ctx context.Context, id uuid.UUID) (*postgres.Stream, error) {
	st, ok := s.streams[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	cp := *st
	return &cp, nil
}

func (s *testStore) GoLive(ctx context.Context, id uuid.UUID) error {
	st, ok := s.streams[id]
	if !ok {
		return postgres.ErrNotFound
	}
	st.Status = "live"
	now := time.Now()
	st.StartedAt = &now
	return nil
}

func (s *testStore) EndStream(ctx context.Context, id uuid.UUID) error {
	st, ok := s.streams[id]
	if !ok {
		return postgres.ErrNotFound
	}
	st.Status = "ended"
	now := time.Now()
	st.EndedAt = &now
	return nil
}

// --- Viewer / Chat / Likes ---

func (s *testStore) UpdateViewerCount(_ context.Context, _ uuid.UUID, _ int) error    { return nil }
func (s *testStore) IncrementLikes(_ context.Context, _ uuid.UUID) error              { return nil }
func (s *testStore) JoinStream(_ context.Context, _, _ uuid.UUID) error               { return nil }
func (s *testStore) LeaveStream(_ context.Context, _, _ uuid.UUID) error              { return nil }
func (s *testStore) GetActiveViewerCount(_ context.Context, _ uuid.UUID) (int, error) { return 1, nil }
func (s *testStore) SendChatMessage(_ context.Context, _ *postgres.ChatMessage) error { return nil }
func (s *testStore) GetChatMessage(_ context.Context, _ uuid.UUID) (*postgres.ChatMessage, error) {
	return &postgres.ChatMessage{
		ID:       uuid.New(),
		StreamID: uuid.New(),
	}, nil
}
func (s *testStore) GetChatMessages(_ context.Context, _ uuid.UUID, _ int, _ *time.Time) ([]postgres.ChatMessage, error) {
	return nil, nil
}
func (s *testStore) PinMessage(_ context.Context, _ uuid.UUID) error { return nil }

// --- Scheduled ---

func (s *testStore) CreateScheduledStream(_ context.Context, _ *postgres.ScheduledStream) error {
	return nil
}
func (s *testStore) ListUpcomingStreams(_ context.Context, _ int) ([]postgres.ScheduledStream, error) {
	return nil, nil
}
func (s *testStore) GetStreamByKey(_ context.Context, _ string) (*postgres.Stream, error) {
	return nil, postgres.ErrNotFound
}
func (s *testStore) ListLiveStreams(_ context.Context, _, _ int) ([]postgres.Stream, error) {
	streams := make([]postgres.Stream, 0, len(s.streams))
	for _, st := range s.streams {
		if st.Status != "live" {
			continue
		}
		cp := *st
		streams = append(streams, cp)
	}
	return streams, nil
}
func (s *testStore) ListHostStreams(_ context.Context, hostID uuid.UUID, _, _ int) ([]postgres.Stream, error) {
	streams := make([]postgres.Stream, 0, len(s.streams))
	for _, st := range s.streams {
		if st.HostID != hostID {
			continue
		}
		cp := *st
		streams = append(streams, cp)
	}
	return streams, nil
}

// --- Mutes ---

func (s *testStore) MuteUser(_ context.Context, streamID, userID, _ uuid.UUID) error {
	s.mutes[s.muteKey(streamID, userID)] = true
	return nil
}
func (s *testStore) UnmuteUser(_ context.Context, streamID, userID uuid.UUID) error {
	delete(s.mutes, s.muteKey(streamID, userID))
	return nil
}
func (s *testStore) GetMutedUsers(_ context.Context, _ uuid.UUID) ([]postgres.LiveMute, error) {
	return nil, nil
}
func (s *testStore) IsUserMuted(_ context.Context, streamID, userID uuid.UUID) (bool, error) {
	return s.mutes[s.muteKey(streamID, userID)], nil
}

// --- Word filters ---

func (s *testStore) AddWordFilter(_ context.Context, streamID uuid.UUID, word string, _ uuid.UUID) error {
	sid := streamID.String()
	s.wordFilters[sid] = append(s.wordFilters[sid], word)
	return nil
}
func (s *testStore) RemoveWordFilter(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (s *testStore) GetWordFilters(_ context.Context, _ uuid.UUID) ([]postgres.LiveWordFilter, error) {
	return nil, nil
}
func (s *testStore) MatchesWordFilter(_ context.Context, streamID uuid.UUID, message string) (bool, error) {
	for _, w := range s.wordFilters[streamID.String()] {
		if len(w) > 0 && len(message) >= len(w) {
			for i := 0; i <= len(message)-len(w); i++ {
				if message[i:i+len(w)] == w {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// --- Polls ---

func (s *testStore) CreateLivePoll(_ context.Context, p *postgres.LivePoll) (*postgres.LivePoll, error) {
	p.ID = uuid.New()
	cp := *p
	s.polls[p.ID] = &cp
	return p, nil
}
func (s *testStore) GetLivePolls(_ context.Context, _ uuid.UUID) ([]postgres.LivePoll, error) {
	return nil, nil
}
func (s *testStore) VoteOnPoll(_ context.Context, pollID, userID uuid.UUID, _ string) error {
	key := s.pollVoteKey(pollID, userID)
	s.pollVotes[key] = true // ON CONFLICT DO NOTHING — just record the first vote
	return nil
}

// --- Gifts ---

func (s *testStore) SendGift(_ context.Context, g *postgres.LiveGift) (*postgres.LiveGift, error) {
	g.ID = uuid.New()
	g.SentAt = time.Now()
	return g, nil
}
func (s *testStore) GetStreamGifts(_ context.Context, _ uuid.UUID, _ int) ([]postgres.LiveGift, error) {
	return nil, nil
}
func (s *testStore) GetGiftLeaderboard(_ context.Context, _ uuid.UUID, _ int) ([]postgres.GiftLeaderboardEntry, error) {
	return nil, nil
}

// --- Guests ---

func (s *testStore) InviteGuest(_ context.Context, streamID, userID uuid.UUID, _ string) error {
	s.guestStatuses[s.guestKey(streamID, userID)] = "invited"
	return nil
}
func (s *testStore) UpdateGuestStatus(_ context.Context, streamID, userID uuid.UUID, status string) error {
	s.guestStatuses[s.guestKey(streamID, userID)] = status
	return nil
}
func (s *testStore) GetStreamGuests(_ context.Context, _ uuid.UUID) ([]postgres.LiveGuest, error) {
	return nil, nil
}

// --- DVR ---

func (s *testStore) AddDVRSegment(_ context.Context, _ *postgres.LiveDVRSegment) error { return nil }
func (s *testStore) GetDVRSegments(_ context.Context, _ uuid.UUID) ([]postgres.LiveDVRSegment, error) {
	return nil, nil
}
func (s *testStore) ExpireDVRSegments(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

// --- Audio rooms ---

func (s *testStore) CreateAudioRoom(_ context.Context, r *postgres.AudioRoom) (*postgres.AudioRoom, error) {
	r.ID = uuid.New()
	return r, nil
}
func (s *testStore) GetAudioRoom(_ context.Context, _ uuid.UUID) (*postgres.AudioRoom, error) {
	return nil, postgres.ErrNotFound
}
func (s *testStore) UpdateAudioRoomStatus(_ context.Context, _ uuid.UUID, _ string, _, _ *time.Time) error {
	return nil
}
func (s *testStore) JoinAudioRoom(_ context.Context, _, _ uuid.UUID, _ string) error { return nil }
func (s *testStore) LeaveAudioRoom(_ context.Context, _, _ uuid.UUID) error          { return nil }
func (s *testStore) GetAudioRoomMembers(_ context.Context, _ uuid.UUID) ([]postgres.AudioRoomMember, error) {
	return nil, nil
}
func (s *testStore) ListLiveAudioRooms(_ context.Context, _ int) ([]postgres.AudioRoom, error) {
	return nil, nil
}

// =============================================================================
// Helpers
// =============================================================================

// newServiceWithStore constructs a Service backed by the in-memory test store.
// Because the test is in package service (not package service_test), it can
// directly assign the unexported store field.
func newServiceWithStore(ts *testStore) *Service {
	return newServiceWithStoreAndConfig(ts, StreamMediaConfig{})
}

func newServiceWithStoreAndConfig(ts *testStore, cfg StreamMediaConfig) *Service {
	// Build a no-op Kafka writer (will not establish a connection during tests)
	w := &kafka.Writer{
		Addr:     kafka.TCP("localhost:9092"),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
	}
	return &Service{
		store:       ts,
		writer:      w,
		mediaConfig: cfg.withDefaults(),
	}
}

func mustLiveStream(store *testStore, hostID uuid.UUID) *postgres.Stream {
	now := time.Now()
	st := &postgres.Stream{
		ID:        uuid.New(),
		HostID:    hostID,
		Title:     "Test Stream",
		StreamKey: "live_testkey",
		Status:    "live",
		StartedAt: &now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	store.streams[st.ID] = st
	return st
}

func mustIdleStream(store *testStore, hostID uuid.UUID) *postgres.Stream {
	now := time.Now()
	st := &postgres.Stream{
		ID:        uuid.New(),
		HostID:    hostID,
		Title:     "Idle Stream",
		StreamKey: "live_idlekey",
		Status:    "idle",
		CreatedAt: now,
		UpdatedAt: now,
	}
	store.streams[st.ID] = st
	return st
}

func mustEndedStream(store *testStore, hostID uuid.UUID) *postgres.Stream {
	now := time.Now()
	st := &postgres.Stream{
		ID:        uuid.New(),
		HostID:    hostID,
		Title:     "Ended Stream",
		StreamKey: "live_endedkey",
		Status:    "ended",
		CreatedAt: now,
		UpdatedAt: now,
	}
	store.streams[st.ID] = st
	return st
}

func twoPollOptions() json.RawMessage {
	opts := []map[string]interface{}{
		{"id": "a", "text": "Option A", "votes": 0},
		{"id": "b", "text": "Option B", "votes": 0},
	}
	data, _ := json.Marshal(opts)
	return data
}

// =============================================================================
// Tests
// =============================================================================

// TestStreamLifecycle verifies the normal idle → live → ended state transitions.
func TestStreamLifecycle(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	svc := newServiceWithStore(ts)
	hostID := uuid.New()

	// Create the stream in idle state
	st := mustIdleStream(ts, hostID)

	// Transition idle → live
	if err := svc.GoLive(ctx, st.ID, hostID); err != nil {
		t.Fatalf("GoLive failed: %v", err)
	}
	loaded := ts.streams[st.ID]
	if loaded.Status != "live" {
		t.Errorf("expected status=live after GoLive, got %q", loaded.Status)
	}
	if loaded.StartedAt == nil {
		t.Error("expected StartedAt to be set after GoLive")
	}

	// Transition live → ended
	if err := svc.EndStream(ctx, st.ID, hostID); err != nil {
		t.Fatalf("EndStream failed: %v", err)
	}
	loaded = ts.streams[st.ID]
	if loaded.Status != "ended" {
		t.Errorf("expected status=ended after EndStream, got %q", loaded.Status)
	}
	if loaded.EndedAt == nil {
		t.Error("expected EndedAt to be set after EndStream")
	}
}

// TestInvalidTransitions verifies forbidden state machine transitions and
// ownership enforcement.
func TestInvalidTransitions(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	svc := newServiceWithStore(ts)
	hostID := uuid.New()
	otherUser := uuid.New()

	// Wrong host cannot go live
	idleStream := mustIdleStream(ts, hostID)
	if err := svc.GoLive(ctx, idleStream.ID, otherUser); err == nil {
		t.Error("expected error when non-host tries GoLive, got nil")
	}

	// Cannot go live on an already-live stream
	liveStream := mustLiveStream(ts, hostID)
	if err := svc.GoLive(ctx, liveStream.ID, hostID); err == nil {
		t.Error("expected error when calling GoLive on an already-live stream, got nil")
	}

	// Cannot end an ended stream
	endedStream := mustEndedStream(ts, hostID)
	if err := svc.EndStream(ctx, endedStream.ID, hostID); err == nil {
		t.Error("expected error when ending an already-ended stream, got nil")
	}

	// Cannot go live on an ended stream
	if err := svc.GoLive(ctx, endedStream.ID, hostID); err == nil {
		t.Error("expected error when calling GoLive on ended stream, got nil")
	}
}

// TestChatModeration verifies that:
//  1. A muted user's chat message is rejected.
//  2. A message matching a word filter is rejected.
//  3. A clean message from an unmuted user passes.
func TestChatModeration(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	svc := newServiceWithStore(ts)
	hostID := uuid.New()
	viewerID := uuid.New()

	st := mustLiveStream(ts, hostID)

	// Clean message succeeds
	if _, err := svc.SendChatMessage(ctx, st.ID, viewerID, "hello world"); err != nil {
		t.Fatalf("expected clean chat to succeed, got: %v", err)
	}

	// Mute the viewer
	ts.mutes[ts.muteKey(st.ID, viewerID)] = true

	// Muted viewer is rejected
	if _, err := svc.SendChatMessage(ctx, st.ID, viewerID, "hello world"); err == nil {
		t.Error("expected muted user to be rejected, got nil error")
	}

	// Unmute and check message passes again
	delete(ts.mutes, ts.muteKey(st.ID, viewerID))
	if _, err := svc.SendChatMessage(ctx, st.ID, viewerID, "hello world"); err != nil {
		t.Fatalf("expected unmuted user's message to pass, got: %v", err)
	}

	// Add a word filter
	ts.wordFilters[st.ID.String()] = []string{"badword"}

	// Message containing filtered word is rejected
	if _, err := svc.SendChatMessage(ctx, st.ID, viewerID, "this is a badword message"); err == nil {
		t.Error("expected word-filtered message to be rejected, got nil error")
	}

	// Clean message still passes
	if _, err := svc.SendChatMessage(ctx, st.ID, viewerID, "totally fine"); err != nil {
		t.Fatalf("expected clean message to pass after word filter added, got: %v", err)
	}
}

// TestPollDuplicateVote verifies that a second vote from the same user is silently
// accepted (idempotent, matching ON CONFLICT DO NOTHING semantics).
func TestPollDuplicateVote(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	svc := newServiceWithStore(ts)
	hostID := uuid.New()
	voterID := uuid.New()

	st := mustLiveStream(ts, hostID)

	poll, err := svc.CreateLivePoll(ctx, &CreatePollInput{
		StreamID: st.ID,
		HostID:   hostID,
		Question: "Which option?",
		Options:  twoPollOptions(),
	})
	if err != nil {
		t.Fatalf("CreateLivePoll failed: %v", err)
	}

	// First vote
	if err := svc.VoteOnPoll(ctx, st.ID, poll.ID, voterID, "a"); err != nil {
		t.Fatalf("first VoteOnPoll failed: %v", err)
	}

	// Second vote on same poll → must not return an error (silently ignored)
	if err := svc.VoteOnPoll(ctx, st.ID, poll.ID, voterID, "b"); err != nil {
		t.Fatalf("duplicate vote must be silently ignored, got: %v", err)
	}

	// Record must exist
	if !ts.pollVotes[ts.pollVoteKey(poll.ID, voterID)] {
		t.Error("expected vote entry to be recorded in test store")
	}
}

// TestGuestStateTransitions verifies valid accept/decline/remove flows and
// ownership enforcement.
func TestGuestStateTransitions(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	svc := newServiceWithStore(ts)
	hostID := uuid.New()
	guestID := uuid.New()
	stranger := uuid.New()

	st := mustLiveStream(ts, hostID)

	// Host invites guest
	if err := svc.InviteGuest(ctx, st.ID, hostID, guestID, "guest"); err != nil {
		t.Fatalf("InviteGuest failed: %v", err)
	}
	if ts.guestStatuses[ts.guestKey(st.ID, guestID)] != "invited" {
		t.Errorf("expected invited status after InviteGuest, got %q",
			ts.guestStatuses[ts.guestKey(st.ID, guestID)])
	}

	// Non-host cannot invite
	if err := svc.InviteGuest(ctx, st.ID, stranger, guestID, "guest"); err == nil {
		t.Error("expected error when non-host invites, got nil")
	}

	// Guest accepts own invitation
	if err := svc.UpdateGuestStatus(ctx, st.ID, guestID, guestID, "accepted"); err != nil {
		t.Fatalf("guest accept failed: %v", err)
	}
	if ts.guestStatuses[ts.guestKey(st.ID, guestID)] != "accepted" {
		t.Errorf("expected accepted status, got %q", ts.guestStatuses[ts.guestKey(st.ID, guestID)])
	}

	// Host cannot accept on behalf of guest
	if err := svc.UpdateGuestStatus(ctx, st.ID, hostID, guestID, "accepted"); err == nil {
		t.Error("expected error when host tries to accept on behalf of guest, got nil")
	}

	// Host removes guest
	if err := svc.UpdateGuestStatus(ctx, st.ID, hostID, guestID, "removed"); err != nil {
		t.Fatalf("host remove failed: %v", err)
	}
	if ts.guestStatuses[ts.guestKey(st.ID, guestID)] != "removed" {
		t.Errorf("expected removed status, got %q", ts.guestStatuses[ts.guestKey(st.ID, guestID)])
	}

	// Stranger cannot remove
	guestID2 := uuid.New()
	_ = svc.InviteGuest(ctx, st.ID, hostID, guestID2, "guest")
	if err := svc.UpdateGuestStatus(ctx, st.ID, stranger, guestID2, "removed"); err == nil {
		t.Error("expected error when stranger tries to remove guest, got nil")
	}

	// Guest declines own invitation
	if err := svc.UpdateGuestStatus(ctx, st.ID, guestID2, guestID2, "declined"); err != nil {
		t.Fatalf("guest decline failed: %v", err)
	}
	if ts.guestStatuses[ts.guestKey(st.ID, guestID2)] != "declined" {
		t.Errorf("expected declined status, got %q", ts.guestStatuses[ts.guestKey(st.ID, guestID2)])
	}
}

func TestGetStreamDecoratesPlaybackAndIngestForHost(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	hostID := uuid.New()
	st := mustLiveStream(ts, hostID)

	svc := newServiceWithStoreAndConfig(ts, StreamMediaConfig{
		PlaybackURLTemplate: "https://stream.cleestudio.com/live/{stream_id}/index.m3u8",
		IngestURL:           "rtmps://stream.cleestudio.com/live",
	})

	got, err := svc.GetStream(ctx, st.ID)
	if err != nil {
		t.Fatalf("GetStream failed: %v", err)
	}

	if got.PlaybackURL == nil {
		t.Fatal("expected playback URL to be populated for a live stream")
	}
	if want := "https://stream.cleestudio.com/live/" + st.ID.String() + "/index.m3u8"; *got.PlaybackURL != want {
		t.Fatalf("expected playback URL %q, got %q", want, *got.PlaybackURL)
	}
	if strings.Contains(*got.PlaybackURL, st.StreamKey) {
		t.Fatalf("playback URL must not leak the secret stream key: %q", *got.PlaybackURL)
	}
	if got.PlaybackProtocol == nil || *got.PlaybackProtocol != "hls" {
		t.Fatalf("expected playback protocol hls, got %#v", got.PlaybackProtocol)
	}
	if got.IngestURL == nil || *got.IngestURL != "rtmps://stream.cleestudio.com/live" {
		t.Fatalf("expected ingest URL to be exposed to the host, got %#v", got.IngestURL)
	}
	if got.IngestProtocol == nil || *got.IngestProtocol != "rtmp" {
		t.Fatalf("expected ingest protocol rtmp, got %#v", got.IngestProtocol)
	}
}

func TestListHostStreamsDecoratesWithoutLeakingSecretsIntoPlayback(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	hostID := uuid.New()
	st := mustLiveStream(ts, hostID)

	svc := newServiceWithStoreAndConfig(ts, StreamMediaConfig{
		PlaybackBaseURL: "https://stream.cleestudio.com/live",
		IngestURL:       "rtmps://stream.cleestudio.com/live",
	})

	streams, err := svc.ListHostStreams(ctx, hostID, 10, 0)
	if err != nil {
		t.Fatalf("ListHostStreams failed: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("expected one host stream, got %d", len(streams))
	}

	got := streams[0]
	if got.StreamKey != st.StreamKey {
		t.Fatalf("expected host list to retain stream key, got %q", got.StreamKey)
	}
	if got.PlaybackURL == nil || strings.Contains(*got.PlaybackURL, st.StreamKey) {
		t.Fatalf("expected public playback URL without stream key, got %#v", got.PlaybackURL)
	}
	if got.IngestURL == nil {
		t.Fatal("expected host list item to include ingest URL")
	}
}

func TestEndedStreamOmitsActiveTransportFields(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	hostID := uuid.New()
	st := mustEndedStream(ts, hostID)
	replayURL := "https://cdn.cleestudio.com/replays/" + st.ID.String() + ".m3u8"
	st.ReplayURL = &replayURL

	svc := newServiceWithStoreAndConfig(ts, StreamMediaConfig{
		PlaybackBaseURL: "https://stream.cleestudio.com/live",
		IngestURL:       "rtmps://stream.cleestudio.com/live",
	})

	got, err := svc.GetStream(ctx, st.ID)
	if err != nil {
		t.Fatalf("GetStream failed: %v", err)
	}
	if got.PlaybackURL != nil {
		t.Fatalf("expected ended stream to omit playback URL, got %q", *got.PlaybackURL)
	}
	if got.IngestURL != nil {
		t.Fatalf("expected ended stream to omit ingest URL, got %q", *got.IngestURL)
	}
	if got.ReplayURL == nil || *got.ReplayURL != replayURL {
		t.Fatalf("expected replay URL to be preserved, got %#v", got.ReplayURL)
	}
}

func TestListLiveStreamsUsesPublicStreamIDPlaybackURL(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	hostID := uuid.New()
	st := mustLiveStream(ts, hostID)

	svc := newServiceWithStoreAndConfig(ts, StreamMediaConfig{
		PlaybackURLTemplate: "https://cleestudio.com/v1/live/streams/{stream_id}/playback/index.m3u8",
	})

	streams, err := svc.ListLiveStreams(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListLiveStreams failed: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("expected one live stream, got %d", len(streams))
	}

	got := streams[0]
	if got.PlaybackURL == nil {
		t.Fatal("expected playback URL to be populated")
	}
	if want := "https://cleestudio.com/v1/live/streams/" + st.ID.String() + "/playback/index.m3u8"; *got.PlaybackURL != want {
		t.Fatalf("expected playback URL %q, got %q", want, *got.PlaybackURL)
	}
	if strings.Contains(*got.PlaybackURL, st.StreamKey) {
		t.Fatalf("public playback URL must not leak stream key, got %q", *got.PlaybackURL)
	}
	if got.StreamKey != "" {
		t.Fatalf("expected public live list to scrub stream key, got %q", got.StreamKey)
	}
}

func TestResolvePlaybackAssetURLUsesInternalOriginAndSecretKey(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	hostID := uuid.New()
	st := mustLiveStream(ts, hostID)

	svc := newServiceWithStoreAndConfig(ts, StreamMediaConfig{
		PlaybackURLTemplate:     "https://cleestudio.com/v1/live/streams/{stream_id}/playback/index.m3u8",
		PlaybackInternalBaseURL: "http://mediamtx:8888",
	})

	target, err := svc.ResolvePlaybackAssetURL(ctx, st.ID, "/segment-001.ts")
	if err != nil {
		t.Fatalf("ResolvePlaybackAssetURL failed: %v", err)
	}

	if want := "http://mediamtx:8888/" + st.StreamKey + "/segment-001.ts"; target.String() != want {
		t.Fatalf("expected internal playback asset URL %q, got %q", want, target.String())
	}
	if strings.Contains(target.String(), st.ID.String()) {
		t.Fatalf("internal playback asset URL should resolve by stream key, got %q", target.String())
	}
}

func TestResolvePlaybackAssetURLRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	ts := newTestStore()
	hostID := uuid.New()
	st := mustLiveStream(ts, hostID)

	svc := newServiceWithStoreAndConfig(ts, StreamMediaConfig{
		PlaybackInternalBaseURL: "http://mediamtx:8888",
	})

	_, err := svc.ResolvePlaybackAssetURL(ctx, st.ID, "/../secret.txt")
	if !errors.Is(err, ErrInvalidPlaybackAssetPath) {
		t.Fatalf("expected ErrInvalidPlaybackAssetPath, got %v", err)
	}
}
