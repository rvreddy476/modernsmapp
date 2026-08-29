package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/google/uuid"
)

// CALL-LB-4: the call delivery orchestration must PROPAGATE every transport
// failure so the durable consumer keeps the event uncommitted — the pre-fix
// path logged realtime/device/push errors and returned nil, permanently
// losing the only wake-up a backgrounded recipient had.

type scriptedCallTransports struct {
	rowErr     error
	realtime   int
	rtErr      error
	targetsErr error
	allowed    bool
	targets    []callPushTarget
	pushErrs   map[string]error
	pushes     []string
	retired    []string
	retireErr  error
}

func (t *scriptedCallTransports) ensureCallRow(context.Context, *scylla.Notification) error {
	return t.rowErr
}

func (t *scriptedCallTransports) publishCallRealtime(context.Context, uuid.UUID, *scylla.Notification) error {
	t.realtime++
	return t.rtErr
}

func (t *scriptedCallTransports) callPushTargets(context.Context, uuid.UUID) ([]callPushTarget, bool, error) {
	return t.targets, t.allowed, t.targetsErr
}

func (t *scriptedCallTransports) sendCallPush(_ context.Context, target callPushTarget, _ string, _ *scylla.Notification) error {
	t.pushes = append(t.pushes, target.Token)
	return t.pushErrs[target.Token]
}

func (t *scriptedCallTransports) retireCallDevice(_ context.Context, _ uuid.UUID, target callPushTarget) error {
	if t.retireErr != nil {
		return t.retireErr
	}
	t.retired = append(t.retired, target.Token)
	return nil
}

func ring(t *scriptedCallTransports) error {
	n := &scylla.Notification{UserID: uuid.New(), EntityID: uuid.New()}
	return runCallDelivery(context.Background(), t, n.UserID, "incoming_call", n)
}

func TestCallDeliveryPropagatesEveryTransportFailure(t *testing.T) {
	cases := []struct {
		name string
		deps *scriptedCallTransports
		want string
	}{
		{"row", &scriptedCallTransports{rowErr: errors.New("scylla down")}, "call notification row"},
		{"realtime", &scriptedCallTransports{rtErr: errors.New("redis down")}, "call realtime publish"},
		{"device lookup", &scriptedCallTransports{targetsErr: errors.New("pg down")}, "call push targets"},
		{
			"push send",
			&scriptedCallTransports{
				allowed:  true,
				targets:  []callPushTarget{{Token: "tok-1", Platform: "android"}},
				pushErrs: map[string]error{"tok-1": errors.New("fcm 503")},
			},
			"call push (android)",
		},
	}
	for _, tc := range cases {
		err := ring(tc.deps)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s failure was swallowed: %v (CALL-LB-4)", tc.name, err)
		}
	}
}

// A retry AFTER the durable row already exists must still run the
// transports — this is exactly the redelivery the review proved was
// unrepairable on the at-most-once path.
func TestCallDeliveryRetryAfterExistingRowStillDelivers(t *testing.T) {
	deps := &scriptedCallTransports{
		allowed: true,
		targets: []callPushTarget{{Token: "tok-1", Platform: "android"}},
	}
	// First attempt: row created, push failed → error.
	deps.pushErrs = map[string]error{"tok-1": errors.New("fcm 503")}
	if err := ring(deps); err == nil {
		t.Fatal("first failing attempt returned nil")
	}
	// Retry: ensureCallRow reports success without applying (row exists) —
	// realtime and push must run AGAIN and now succeed.
	deps.pushErrs = nil
	if err := ring(deps); err != nil {
		t.Fatalf("retry after existing row failed: %v", err)
	}
	if deps.realtime != 2 || len(deps.pushes) != 2 {
		t.Fatalf("transports did not re-run on retry: realtime=%d pushes=%v (CALL-LB-4)",
			deps.realtime, deps.pushes)
	}
}

// Push disabled (preferences / quiet hours / unconfigured) is a SUCCESS, not
// a failure — the ring must not wedge on a user who turned pushes off.
func TestCallDeliveryPushDisallowedIsNotAFailure(t *testing.T) {
	deps := &scriptedCallTransports{allowed: false}
	if err := ring(deps); err != nil {
		t.Fatalf("push-disallowed treated as failure: %v", err)
	}
	if len(deps.pushes) != 0 {
		t.Fatalf("push sent despite disallow: %v", deps.pushes)
	}
}

// CALL-LB-4 (provider boundary): a PERMANENTLY rejected token is retired
// durably and the delivery COMPLETES — success is what lets the consumer
// commit and the partition advance; the pre-fix behavior retried the dead
// token forever and stalled every later call event.
func TestCallDeliveryStaleTokenIsRetiredAndDeliveryCompletes(t *testing.T) {
	deps := &scriptedCallTransports{
		allowed:  true,
		targets:  []callPushTarget{{Token: "stale-1", Platform: "android"}},
		pushErrs: map[string]error{"stale-1": fmt.Errorf("fcm: status 404: %w", push.ErrDeviceRejected)},
	}
	if err := ring(deps); err != nil {
		t.Fatalf("stale token stalled the delivery: %v (CALL-LB-4)", err)
	}
	if len(deps.retired) != 1 || deps.retired[0] != "stale-1" {
		t.Fatalf("rejected device not retired: %v", deps.retired)
	}
}

// Mixed devices: the stale token is retired, the valid device still gets its
// ring, and the delivery completes.
func TestCallDeliveryMixedDevicesRetireStaleAndDeliverValid(t *testing.T) {
	for _, order := range [][]callPushTarget{
		{{Token: "stale-1", Platform: "android"}, {Token: "valid-1", Platform: "android"}},
		{{Token: "valid-1", Platform: "android"}, {Token: "stale-1", Platform: "android"}},
	} {
		deps := &scriptedCallTransports{
			allowed:  true,
			targets:  order,
			pushErrs: map[string]error{"stale-1": fmt.Errorf("apns: status 410: %w", push.ErrDeviceRejected)},
		}
		if err := ring(deps); err != nil {
			t.Fatalf("mixed devices stalled the delivery: %v (CALL-LB-4)", err)
		}
		if len(deps.retired) != 1 || deps.retired[0] != "stale-1" {
			t.Fatalf("stale device not retired: %v", deps.retired)
		}
		if len(deps.pushes) != 2 {
			t.Fatalf("valid device skipped: pushes=%v", deps.pushes)
		}
	}
}

// Retirement is DURABLE evidence: if it cannot be persisted, the event must
// stay uncommitted — a lost retirement resurrects the stall on redelivery.
func TestCallDeliveryRetireFailureKeepsTheEventUncommitted(t *testing.T) {
	deps := &scriptedCallTransports{
		allowed:   true,
		targets:   []callPushTarget{{Token: "stale-1", Platform: "android"}},
		pushErrs:  map[string]error{"stale-1": fmt.Errorf("fcm: status 404: %w", push.ErrDeviceRejected)},
		retireErr: errors.New("pg down"),
	}
	err := ring(deps)
	if err == nil || !strings.Contains(err.Error(), "retire rejected device") {
		t.Fatalf("failed retirement was swallowed: %v (CALL-LB-4)", err)
	}
}

// A transient provider failure is NOT a rejection: nothing is retired and
// the error propagates so the event stays uncommitted.
func TestCallDeliveryTransientOutageIsNotRetired(t *testing.T) {
	deps := &scriptedCallTransports{
		allowed:  true,
		targets:  []callPushTarget{{Token: "tok-1", Platform: "android"}},
		pushErrs: map[string]error{"tok-1": errors.New("fcm: send failed with status 503")},
	}
	err := ring(deps)
	if err == nil || !strings.Contains(err.Error(), "call push (android)") {
		t.Fatalf("transient outage did not keep the event uncommitted: %v", err)
	}
	if len(deps.retired) != 0 {
		t.Fatalf("transient outage retired a healthy device: %v (CALL-LB-4)", deps.retired)
	}
}

// The REQUIRED posture fails closed on a missing transport; the dev posture
// still skips quietly. Exercised on the real *Service adapters.
func TestCallPushTargetsFailClosedWhenRequired(t *testing.T) {
	required := &Service{callPushRequired: true}
	if _, _, err := required.callPushTargets(context.Background(), uuid.New()); err == nil ||
		!errors.Is(err, push.ErrProviderNotConfigured) {
		t.Fatalf("required mode did not fail closed on missing transport: %v (CALL-LB-4)", err)
	}
	dev := &Service{}
	targets, allowed, err := dev.callPushTargets(context.Background(), uuid.New())
	if err != nil || allowed || len(targets) != 0 {
		t.Fatalf("dev posture changed: %v %v %v", targets, allowed, err)
	}
}

// A platform whose provider is absent: required mode propagates (fail
// closed, uncommitted); dev mode skips loudly without failing.
func TestSendCallPushMissingProviderRequiredVsDev(t *testing.T) {
	n := &scylla.Notification{UserID: uuid.New(), EntityID: uuid.New()}
	target := callPushTarget{Token: "tok-ios", Platform: "ios"}
	noProviders := push.NewDispatcher(nil, nil)

	required := &Service{pusher: noProviders, callPushRequired: true}
	if err := required.sendCallPush(context.Background(), target, "incoming_call", n); err == nil ||
		!errors.Is(err, push.ErrProviderNotConfigured) {
		t.Fatalf("required mode committed an unpushed ring: %v (CALL-LB-4)", err)
	}

	dev := &Service{pusher: noProviders}
	if err := dev.sendCallPush(context.Background(), target, "incoming_call", n); err != nil {
		t.Fatalf("dev posture failed on missing provider: %v", err)
	}
}

// Every device must receive the ring; a second device's failure propagates
// even when the first succeeded.
func TestCallDeliverySecondDeviceFailurePropagates(t *testing.T) {
	deps := &scriptedCallTransports{
		allowed: true,
		targets: []callPushTarget{
			{Token: "tok-1", Platform: "android"},
			{Token: "tok-2", Platform: "ios"},
		},
		pushErrs: map[string]error{"tok-2": errors.New("apns 500")},
	}
	err := ring(deps)
	if err == nil || !strings.Contains(err.Error(), "call push (ios)") {
		t.Fatalf("second-device failure swallowed: %v", err)
	}
}
