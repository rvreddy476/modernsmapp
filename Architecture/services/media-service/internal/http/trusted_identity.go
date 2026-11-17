package http

import (
	"crypto/hmac"

	"github.com/gin-gonic/gin"
)

// reels-6: X-User-Id is believed only from a hop that can prove it set it.
//
// The read routes (/v1/media/:id, /url, /serve, /hls, /download, /batch, the
// audio, subtitle and rendition reads) took the viewer from a bare X-User-Id
// header. The api-gateway does not stamp the internal key on /v1/media,
// /v1/audio, /v1/audio-library or /v1/subtitles, and nothing in the cluster
// enforces a network policy, so any workload in the cluster could send
// `X-User-Id: <author>` and read private, pending or deleted-but-unpurged
// media as its owner.
//
// TrustedViewerIdentity runs before every route. A request is believed about
// its user only when one of these holds:
//
//  1. It carries the internal service key. Every in-cluster caller that
//     forwards a viewer (post, feed, search, commerce, chat message-service)
//     sends the key with X-User-Id; the header is kept as sent.
//  2. It carries an access token (Authorization: Bearer or the access_token
//     cookie, both of which the gateway forwards unchanged) that verifies
//     under this service's key set and policy. X-User-Id is then set to the
//     token's subject, whatever the request said.
//
// Anything else is anonymous: X-User-Id and X-Verified-User-Id are removed,
// so the delivery gate decides as for a signed-out viewer and routes that
// need a session answer 401 (authMW still verifies the token itself). A token
// that fails to verify is treated as no token here rather than a 401: the
// write routes refuse it in authMW, and a read falls back to what an
// anonymous viewer may see. The gateway refuses such a token before it gets
// here, so on the edge path the two cannot disagree.
//
// The future gateway identity assertion (docs/harness/
// design-gateway-identity-2026-11-08.md) leaves media on its own JWT check;
// this is that check applied to the reads.
func TrustedViewerIdentity(keys JWTKeySet, internalKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if internalKey != "" {
			if presented := c.GetHeader(internalKeyHeader); presented != "" &&
				hmac.Equal([]byte(presented), []byte(internalKey)) {
				c.Next()
				return
			}
		}
		c.Request.Header.Del("X-User-Id")
		c.Request.Header.Del("X-Verified-User-Id")
		if raw := extractToken(c); raw != "" {
			if claims, err := verifyJWTClaims(raw, keys); err == nil {
				c.Request.Header.Set("X-User-Id", claims.subject())
			}
		}
		c.Next()
	}
}
