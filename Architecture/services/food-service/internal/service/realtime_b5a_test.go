package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/realtime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type capturedEvent struct {
	topic, eventType string
	raw              []byte
}

// payloadCapture records every realtime publish with its JSON payload.
type payloadCapture struct{ events []capturedEvent }

func (c *payloadCapture) Publish(_ context.Context, topic, eventType string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	c.events = append(c.events, capturedEvent{topic, eventType, raw})
	return nil
}

// outboxCapture records every outbox row.
type outboxCapture struct{ events []capturedEvent }

func (c *outboxCapture) sink(_ context.Context, eventType, key string, body []byte) error {
	c.events = append(c.events, capturedEvent{key, eventType, append([]byte(nil), body...)})
	return nil
}

func capturingService(st Store) (*Service, *payloadCapture, *outboxCapture) {
	svc := New(st)
	rt, ob := &payloadCapture{}, &outboxCapture{}
	svc.rtPublisher = rt
	svc.outboxSink = ob.sink
	return svc, rt, ob
}

// ─── 1. Publisher: Redis Streams, the transport the SSE gateway reads ─────

// What food-service publishes must come out of realtime.NewStreamSubscriber,
// exactly as notification-service's GET /v1/realtime/sse reads it. A Pub/Sub
// PUBLISH never would.
func TestRealtimePublisherReachesTheSSEStreamReader(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	orderID := uuid.New()
	topic := orderTopic(orderID)
	svc := New(nil).WithRealtime(NewRealtimePublisher(rdb), realtime.NewTokenSigner([]byte("test-only-secret")))
	svc.publishRealtime(context.Background(), topic, "food.delivery.picked_up", map[string]any{"order_id": orderID.String()})

	sub := realtime.NewStreamSubscriber(rdb, []string{topic}, "0", 200*time.Millisecond)
	events, err := sub.Read(context.Background())
	if err != nil {
		t.Fatalf("XREAD: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("stream reader saw %d events, want 1 (keys in redis: %v)", len(events), mr.Keys())
	}
	ev := events[0]
	if ev.Topic != topic || ev.Event.EventType != "food.delivery.picked_up" || ev.Event.Topic != topic {
		t.Fatalf("event = %+v", ev)
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Event.Data, &data); err != nil || data["order_id"] != orderID.String() {
		t.Fatalf("payload = %s (%v)", ev.Event.Data, err)
	}
}

// No production code in food-service may publish or subscribe over Redis
// Pub/Sub; the gateway only reads Streams.
func TestFoodServiceNeverUsesRedisPubSub(t *testing.T) {
	root := filepath.Join("..", "..")
	forbidden := []string{"realtime.NewPublisher(", "realtime.Channel(", ".PSubscribe(", ".Subscribe(ctx", "rdb.Publish("}
	scanned := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			for _, f := range forbidden {
				if strings.Contains(string(src), f) {
					t.Errorf("%s uses %q: food realtime must go through NewRealtimePublisher (Redis Streams)", path, f)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if scanned < 20 {
		t.Fatalf("scanned only %d files; the walk is broken", scanned)
	}
}

// ─── 2. Scoped tokens ───────────────────────────────────────────────────────

type tokenFakeStore struct {
	Store
	customer, order, owner, restaurant, rider, riderRow uuid.UUID
}

func (f *tokenFakeStore) GetOrder(_ context.Context, user, order uuid.UUID) (*postgres.Order, error) {
	if user == f.customer && order == f.order {
		return &postgres.Order{ID: order, UserID: user}, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *tokenFakeStore) ListPartnerRestaurants(_ context.Context, user uuid.UUID) ([]postgres.PartnerRestaurant, error) {
	if user == f.owner {
		return []postgres.PartnerRestaurant{{ID: f.restaurant, OwnerUserID: user}}, nil
	}
	return nil, nil
}

func (f *tokenFakeStore) GetDeliveryPartner(_ context.Context, user uuid.UUID) (*postgres.DeliveryPartner, error) {
	if user == f.rider {
		return &postgres.DeliveryPartner{ID: f.riderRow, UserID: user}, nil
	}
	return nil, pgx.ErrNoRows
}

func newTokenFake() *tokenFakeStore {
	return &tokenFakeStore{customer: uuid.New(), order: uuid.New(), owner: uuid.New(), restaurant: uuid.New(), rider: uuid.New(), riderRow: uuid.New()}
}

func tokenExpiryUnix(t *testing.T, tok string) int64 {
	t.Helper()
	parts := strings.SplitN(tok, ".", 2)
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	exp, err := strconv.ParseInt(strings.SplitN(string(payload), "|", 2)[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

func TestRealtimeTokenGrantsOnlyTheCallersOwnScope(t *testing.T) {
	secret := []byte("test-only-secret")
	f := newTokenFake()
	svc := New(f).WithRealtime(&payloadCapture{}, realtime.NewTokenSigner(secret))
	verifier := realtime.NewTokenVerifier(secret)
	ctx := context.Background()
	idp := func(id uuid.UUID) *uuid.UUID { return &id }

	grant := func(user uuid.UUID, scope string, id *uuid.UUID, want []string, deny ...string) {
		t.Helper()
		before := time.Now()
		tok, err := svc.IssueRealtimeToken(ctx, user, scope, id)
		if err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		allowed, subject, err := verifier.Verify(tok.Token)
		if err != nil || subject != user.String() {
			t.Fatalf("%s: verify subject=%s err=%v", scope, subject, err)
		}
		if !reflect.DeepEqual(allowed, want) || !reflect.DeepEqual(tok.Topics, want) {
			t.Fatalf("%s: token grants %v (response %v), want %v", scope, allowed, tok.Topics, want)
		}
		for _, d := range deny {
			if realtime.MatchTopic(allowed, d) {
				t.Fatalf("%s: token also opens %s", scope, d)
			}
		}
		exp := tokenExpiryUnix(t, tok.Token)
		if max := before.Add(RealtimeTokenTTL).Unix() + 2; exp > max || exp < before.Add(RealtimeTokenTTL).Unix()-2 {
			t.Fatalf("%s: token expiry %d not within the %v TTL", scope, exp, RealtimeTokenTTL)
		}
		reported, err := time.Parse(time.RFC3339, tok.ExpiresAt)
		if err != nil || reported.Unix() > exp || tok.TTLSeconds != 300 {
			t.Fatalf("%s: expires_at %q ttl %d vs token exp %d", scope, tok.ExpiresAt, tok.TTLSeconds, exp)
		}
	}

	other := uuid.New()
	grant(f.customer, RealtimeScopeOrder, idp(f.order), []string{"food.order." + f.order.String()},
		"food.order."+other.String(), "food.admin.live_orders", deliveryPartnerTopic(f.customer))
	grant(f.owner, RealtimeScopeRestaurant, idp(f.restaurant),
		[]string{"food.restaurant." + f.restaurant.String() + ".orders", "food.restaurant." + f.restaurant.String()},
		"food.restaurant."+other.String()+".orders")
	grant(f.rider, RealtimeScopeDelivery, nil, []string{deliveryPartnerTopic(f.rider)},
		"food.delivery_partner."+f.riderRow.String()+".assignments")

	stranger := uuid.New()
	refusals := []struct {
		name  string
		user  uuid.UUID
		scope string
		id    *uuid.UUID
		want  error
	}{
		{"foreign order", stranger, RealtimeScopeOrder, idp(f.order), pgx.ErrNoRows},
		{"own customer, other order", f.customer, RealtimeScopeOrder, idp(other), pgx.ErrNoRows},
		{"foreign restaurant", stranger, RealtimeScopeRestaurant, idp(f.restaurant), pgx.ErrNoRows},
		{"owner, other restaurant", f.owner, RealtimeScopeRestaurant, idp(other), pgx.ErrNoRows},
		{"not a delivery partner", f.customer, RealtimeScopeDelivery, nil, pgx.ErrNoRows},
		{"scope missing", f.customer, "", nil, ErrRealtimeScopeInvalid},
		{"unknown scope", f.customer, "admin", idp(f.order), ErrRealtimeScopeInvalid},
		{"order without id", f.customer, RealtimeScopeOrder, nil, ErrRealtimeIDRequired},
		{"restaurant without id", f.owner, RealtimeScopeRestaurant, nil, ErrRealtimeIDRequired},
		{"delivery with id", f.rider, RealtimeScopeDelivery, idp(f.riderRow), ErrRealtimeIDNotAllowed},
	}
	for _, r := range refusals {
		tok, err := svc.IssueRealtimeToken(ctx, r.user, r.scope, r.id)
		if !errors.Is(err, r.want) || tok != nil {
			t.Fatalf("%s: token=%v err=%v, want %v", r.name, tok, err, r.want)
		}
	}

	if _, err := New(f).IssueRealtimeToken(ctx, f.customer, RealtimeScopeOrder, idp(f.order)); !errors.Is(err, ErrRealtimeNotConfigured) {
		t.Fatalf("unwired realtime: %v", err)
	}
}

// ─── 3. Rider location fan-out ─────────────────────────────────────────────

type locationFakeStore struct {
	Store
	res *postgres.DeliveryLocationResult
}

func (f *locationFakeStore) UpdateDeliveryLocation(context.Context, uuid.UUID, postgres.LocationUpdate) (*postgres.DeliveryLocationResult, error) {
	return f.res, nil
}

func TestUpdateDeliveryLocationPublishesOneFramePerClearedOrder(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	heading := 135.0
	recorded := time.Date(2026, 9, 13, 6, 30, 5, 0, time.UTC)
	res := &postgres.DeliveryLocationResult{
		Latitude: 12.9716, Longitude: 77.5946, Heading: &heading, RecordedAtTime: recorded,
		AssignmentIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		Frames:        []postgres.RiderLocationFrame{{OrderID: a}, {OrderID: b}},
	}
	svc, rt, ob := capturingService(&locationFakeStore{res: res})
	if _, err := svc.UpdateDeliveryLocation(context.Background(), uuid.New(), postgres.LocationUpdate{}); err != nil {
		t.Fatal(err)
	}
	if len(rt.events) != 2 {
		t.Fatalf("published %d frames, want one per cleared order", len(rt.events))
	}
	topics := []string{rt.events[0].topic, rt.events[1].topic}
	sort.Strings(topics)
	want := []string{orderTopic(a), orderTopic(b)}
	sort.Strings(want)
	if !reflect.DeepEqual(topics, want) {
		t.Fatalf("topics = %v, want %v", topics, want)
	}
	for _, ev := range rt.events {
		if ev.eventType != EventRiderLocation {
			t.Fatalf("event type %s", ev.eventType)
		}
		var frame map[string]any
		if err := json.Unmarshal(ev.raw, &frame); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(frame))
		for k := range frame {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, []string{"heading", "lat", "lng", "order_id", "recorded_at"}) {
			t.Fatalf("frame keys = %v", keys)
		}
		if frame["recorded_at"] != "2026-09-13T06:30:05Z" || frame["lat"] != 12.9716 || frame["heading"] != 135.0 {
			t.Fatalf("frame = %v", frame)
		}
	}
	if len(ob.events) != 0 {
		t.Fatalf("rider.location went to the outbox: %v", ob.events)
	}

	// No cleared order (outside the window or throttled): nothing goes out.
	res.Frames, res.Heading = nil, nil
	rt.events = nil
	if _, err := svc.UpdateDeliveryLocation(context.Background(), uuid.New(), postgres.LocationUpdate{}); err != nil {
		t.Fatal(err)
	}
	if len(rt.events) != 0 {
		t.Fatalf("published without a cleared order: %v", rt.events)
	}
}

// ─── 4. OTPs never travel on events ────────────────────────────────────────

type acceptFakeStore struct {
	Store
	batch *postgres.DeliveryBatch
	offer *postgres.DeliveryOffer
}

func (f *acceptFakeStore) AcceptBatchOfferTx(context.Context, uuid.UUID, uuid.UUID) (*postgres.DeliveryBatch, uuid.UUID, error) {
	if f.batch != nil {
		return f.batch, uuid.New(), nil
	}
	return nil, uuid.Nil, postgres.ErrNotBatchOffer
}

func (f *acceptFakeStore) AcceptDeliveryOfferTx(context.Context, uuid.UUID, uuid.UUID) (*postgres.DeliveryOffer, error) {
	return f.offer, nil
}

func (f *acceptFakeStore) EnsureDeliveryCodes(context.Context, uuid.UUID) (string, string, error) {
	return "4821", "7390", nil
}

func (f *acceptFakeStore) VerifyPickupCode(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

func assertNoOTPs(t *testing.T, where string, events []capturedEvent) {
	t.Helper()
	for _, ev := range events {
		if field, bad := otpFieldIn(json.RawMessage(ev.raw)); bad {
			t.Fatalf("%s %s on %s carries %s: %s", where, ev.eventType, ev.topic, field, ev.raw)
		}
		for _, code := range []string{"4821", "7390"} {
			if strings.Contains(string(ev.raw), code) {
				t.Fatalf("%s %s on %s carries a code value: %s", where, ev.eventType, ev.topic, ev.raw)
			}
		}
	}
}

func countEvents(events []capturedEvent, eventType string) int {
	n := 0
	for _, ev := range events {
		if ev.eventType == eventType {
			n++
		}
	}
	return n
}

// The assignment events still go out on realtime, never with a code. Since
// B5c the Kafka copy is written by the store inside the accept transaction
// (TestOrderEventsCommitWithTheirTransition), so the service's own outbox leg
// stays empty.
func TestDeliveryEventsNeverCarryTheOTPs(t *testing.T) {
	ctx := context.Background()
	single := &acceptFakeStore{offer: &postgres.DeliveryOffer{ID: uuid.New(), OrderID: uuid.New(), DeliveryPartnerID: uuid.New(), Status: "accepted"}}
	batch := &acceptFakeStore{batch: &postgres.DeliveryBatch{ID: uuid.New(), RestaurantID: uuid.New(), Status: "assigned",
		Members: []postgres.BatchMember{{OrderID: uuid.New(), Sequence: 1}, {OrderID: uuid.New(), Sequence: 2}}}}

	for name, tc := range map[string]struct {
		st   *acceptFakeStore
		want int
	}{"single": {single, 1}, "batch": {batch, 2}} {
		svc, rt, ob := capturingService(tc.st)
		if err := svc.AcceptDeliveryOffer(ctx, uuid.New(), uuid.New()); err != nil {
			t.Fatalf("%s accept: %v", name, err)
		}
		if countEvents(rt.events, "food.delivery.assigned") != tc.want || countEvents(ob.events, "food.delivery.assigned") != 0 {
			t.Fatalf("%s: food.delivery.assigned realtime=%d (want %d; an OTP-bearing payload is refused) service outbox=%d (want 0: the store writes it in the accept tx)",
				name, countEvents(rt.events, "food.delivery.assigned"), tc.want, countEvents(ob.events, "food.delivery.assigned"))
		}
		assertNoOTPs(t, name+" realtime", rt.events)
		assertNoOTPs(t, name+" outbox", ob.events)

		if err := svc.VerifyPickupCode(ctx, uuid.New(), uuid.New(), "4821"); err != nil {
			t.Fatal(err)
		}
		assertNoOTPs(t, name+" realtime", rt.events)
		assertNoOTPs(t, name+" outbox", ob.events)
	}
}

// The guard itself: any payload with an OTP key, at any depth, is refused on
// both legs; a clean payload goes out on both.
func TestEmitRefusesAnyPayloadCarryingAnOTP(t *testing.T) {
	ctx := context.Background()
	svc, rt, ob := capturingService(nil)
	for _, data := range []any{
		map[string]any{"pickup_code": "1111"},
		map[string]any{"order": map[string]any{"delivery_code": "2222"}},
		[]any{map[string]any{"pickup_otp": "3333"}},
		map[string]any{"items": []any{map[string]any{"delivery_otp": "4444"}}},
		postgres.DeliveryAssignment{ID: uuid.New(), PickupCode: "5555"},
		postgres.Order{ID: uuid.New(), DeliveryCode: "6666"},
	} {
		svc.emit(ctx, "food.order.x", "food.delivery.assigned", data)
		svc.publishRealtime(ctx, "food.order.x", "food.delivery.assigned", data)
		if len(rt.events) != 0 || len(ob.events) != 0 {
			t.Fatalf("payload %#v went out: realtime=%d outbox=%d", data, len(rt.events), len(ob.events))
		}
	}
	svc.emit(ctx, "food.order.x", "food.order.placed", postgres.Order{ID: uuid.New()})
	if len(rt.events) != 1 || len(ob.events) != 1 {
		t.Fatalf("clean payload: realtime=%d outbox=%d", len(rt.events), len(ob.events))
	}
}

// ─── 5. Presence worker pass ───────────────────────────────────────────────

type presenceFakeStore struct {
	Store
	offline, purge     int
	silence, retention time.Duration
}

func (f *presenceFakeStore) AutoOfflineStaleDeliveryPartners(_ context.Context, silence time.Duration) ([]uuid.UUID, error) {
	f.offline++
	f.silence = silence
	return nil, nil
}

func (f *presenceFakeStore) PurgeDeliveryLocationHistory(_ context.Context, olderThan time.Duration, _ int) (postgres.LocationPurgeResult, error) {
	f.purge++
	f.retention = olderThan
	return postgres.LocationPurgeResult{}, nil
}

func TestRiderPresencePass(t *testing.T) {
	f := &presenceFakeStore{}
	svc := New(f)
	for tick := 0; tick <= locationPurgeEveryTicks; tick++ {
		svc.runRiderPresencePass(context.Background(), tick)
	}
	if f.offline != locationPurgeEveryTicks+1 || f.silence != 5*time.Minute {
		t.Fatalf("auto-offline ran %d times with %v", f.offline, f.silence)
	}
	if f.purge != 2 || f.retention != 30*24*time.Hour {
		t.Fatalf("purge ran %d times with %v, want on tick 0 and tick %d with 30 days", f.purge, f.retention, locationPurgeEveryTicks)
	}
}
