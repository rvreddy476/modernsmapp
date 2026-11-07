// Login anomaly detection at sign-in: probing the new sign-in against the
// account's history, applying the side effects (audit row, last-IP cache,
// event), and the owner's list and acknowledge calls. Pure move out of
// auth.go; behaviour is unchanged.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// anomalyProbeResult captures everything classifyAnomalyRisk needs to
// make the enforcement call. We return signals rather than executing
// side effects so the caller can decide (based on LOGIN_ANOMALY_ENFORCE)
// whether to issue the session or stash a step-up.
type anomalyProbeResult struct {
	IsNewIP         bool
	IsNewDevice     bool
	LastIP          string
	UAFamilyChanged bool
}

// probeLoginAnomaly runs the cheap "is this novel?" checks WITHOUT
// recording the anomaly row or publishing the Kafka event. Returns
// enough signal for classifyAnomalyRisk to grade the attempt. Side
// effects (RecordLoginAnomaly + Kafka publish + last-IP refresh) are
// deferred to applyAnomalySideEffects so we can suppress the last-IP
// refresh on enforce-blocked logins (otherwise a successful step-up
// would still see the IP as "new" the second time round).
func (s *Service) probeLoginAnomaly(ctx context.Context, userID, ip, deviceID string) anomalyProbeResult {
	lastIPKey := fmt.Sprintf("last_ip:%s", userID)
	lastIP, _ := s.rdb.Get(ctx, lastIPKey).Result()
	// An empty ip is no signal, as on refresh: not a new address.
	isNewIP := lastIP != "" && ip != "" && lastIP != ip

	isNewDevice := false
	if deviceID != "" {
		if uid, err := uuid.Parse(userID); err == nil {
			devices, derr := s.store.ListTrustedDevices(ctx, uid)
			if derr == nil {
				seen := false
				for _, d := range devices {
					if d.DeviceFingerprint == deviceID {
						seen = true
						break
					}
				}
				isNewDevice = !seen
			}
		}
	}

	return anomalyProbeResult{
		IsNewIP:     isNewIP,
		IsNewDevice: isNewDevice,
		LastIP:      lastIP,
		// UA family change is computed by the refresh path against the
		// stored session; at login time we don't have a prior UA to
		// diff against here, so this stays false. Reserved for future
		// enrichment when we cache last-UA per user.
		UAFamilyChanged: false,
	}
}

// applyAnomalySideEffects is the legacy "shadow" behaviour: persist
// the audit row, refresh the last-IP cache, emit the Kafka event. Run
// when (a) the risk is low/medium and the session is being issued, or
// (b) enforcement mode is "shadow" so we keep doing exactly what we
// did before.
func (s *Service) applyAnomalySideEffects(ctx context.Context, userID, ip, deviceID, platform, userAgent string, probe anomalyProbeResult) {
	lastIPKey := fmt.Sprintf("last_ip:%s", userID)
	// Always refresh the cache — even on no-change logins this resets
	// the TTL so a long-lived account keeps its baseline. Never with an
	// empty ip: that would erase the baseline the next login is graded on.
	if ip != "" {
		s.rdb.Set(ctx, lastIPKey, ip, 30*24*time.Hour)
	}

	if !(probe.IsNewIP || probe.IsNewDevice) {
		return
	}

	if uid, err := uuid.Parse(userID); err == nil {
		anomalyType := "new_ip"
		risk := 30
		if probe.IsNewDevice {
			anomalyType = "new_device"
			risk = 50
		}
		_ = s.store.RecordLoginAnomaly(ctx, uid, anomalyType, ip, userAgent, deviceID, "", risk, false, map[string]any{
			"platform": platform,
			"prior_ip": probe.LastIP,
		})
	}

	payload := map[string]interface{}{
		"user_id":       userID,
		"ip":            ip,
		"device_id":     deviceID,
		"platform":      platform,
		"is_new_ip":     probe.IsNewIP,
		"is_new_device": probe.IsNewDevice,
		"occurred_at":   time.Now(),
	}
	if payloadBytes, err := json.Marshal(payload); err == nil {
		_ = s.producer.PublishRaw(ctx, "user.login_anomaly", userID, json.RawMessage(payloadBytes))
	}
}

// detectLoginAnomaly preserves the original audit-only contract for
// callers that don't want to thread the enforcement gate (currently
// only used by VerifyOTP / LoginWithPassword AFTER the createSession
// path; on enforce mode those paths route through createSessionForUser
// which probes + enforces directly).
//
// Industry-standard split:
//   - Persist for audit + user-visible history.
//   - Emit Kafka event for downstream services (notifications, fraud
//     scoring, ops alerting).
//   - Cache the latest IP in Redis so this hot-path check stays sub-ms
//     even at billions-of-users scale.
func (s *Service) detectLoginAnomaly(ctx context.Context, userID, ip, deviceID, platform, userAgent string) {
	probe := s.probeLoginAnomaly(ctx, userID, ip, deviceID)
	s.applyAnomalySideEffects(ctx, userID, ip, deviceID, platform, userAgent, probe)
}

// ListMyAnomalies powers the user-facing security inbox. Caller is
// resolved from the JWT (handler layer).
func (s *Service) ListMyAnomalies(ctx context.Context, userID uuid.UUID, limit int) ([]store.LoginAnomaly, error) {
	return s.store.ListLoginAnomalies(ctx, userID, limit)
}

// AcknowledgeMyAnomaly lets the user clear an entry from the inbox.
// Idempotent: a second call on an already-acknowledged row no-ops.
func (s *Service) AcknowledgeMyAnomaly(ctx context.Context, userID, anomalyID uuid.UUID) error {
	_, err := s.store.AcknowledgeAnomaly(ctx, userID, anomalyID)
	return err
}
