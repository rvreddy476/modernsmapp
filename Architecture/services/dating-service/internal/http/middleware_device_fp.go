// Device-fingerprint capture middleware — §P0-7 Phase B.
//
// On every pulse/spark request that carries an X-Device-Fingerprint
// header AND a valid X-User-Id, upsert a row into
// dating_device_fingerprints. The risk-scoring job later reads these
// rows to compute the device-reuse + IP/ASN-velocity signals (both
// were 0-weighted scaffolds in Phase A).
//
// The middleware is intentionally best-effort:
//   - Missing X-Device-Fingerprint → no-op (older mobile clients,
//     server-side callers without device context).
//   - Missing/invalid X-User-Id → no-op (the handler will 401 itself).
//   - Upsert error → log + continue. Risk scoring is decisional, not
//     authentication; one missed write should not bounce a pulse load.
//
// Runs *before* the per-route handler so a successful pulse fetch
// stamps last_seen_at on the same request that consumed the deck.
package http

import (
	"log/slog"
	"net"
	"strings"

	"github.com/atpost/dating-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DeviceFingerprintMiddleware returns a Gin middleware that upserts
// (user_id, fingerprint) into dating_device_fingerprints. Bound to
// the service so the middleware can reach the underlying store via
// the existing Service handle (no separate store injection).
func DeviceFingerprintMiddleware(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer c.Next()

		fp := c.GetHeader("X-Device-Fingerprint")
		if fp == "" {
			return
		}
		raw := c.GetHeader("X-User-ID")
		if raw == "" {
			raw = c.GetHeader("X-User-Id")
		}
		if raw == "" {
			return
		}
		userID, err := uuid.Parse(raw)
		if err != nil || userID == uuid.Nil {
			return
		}
		ip := clientIPForFingerprint(c)
		// Use a detached context so the upsert isn't cancelled by a
		// fast handler reply — the row should land even when the
		// client disconnects.
		ctx := c.Request.Context()
		if err := svc.Store().UpsertDeviceFingerprint(ctx, userID, fp, ip); err != nil {
			slog.Warn("device fingerprint upsert failed",
				"user_id", userID, "fingerprint_len", len(fp), "error", err)
		}
	}
}

// clientIPForFingerprint resolves the public IP for the request. The
// gateway forwards X-Forwarded-For; gin.Context.ClientIP() consults
// it when TrustedProxies is configured. We fall back to the raw
// RemoteAddr (host:port → host) for robustness.
func clientIPForFingerprint(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	if c.Request != nil {
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err == nil {
			return host
		}
		// Fall back to the raw value (some test contexts pass a bare
		// host without a port).
		return strings.TrimSpace(c.Request.RemoteAddr)
	}
	return ""
}
