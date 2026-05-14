package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// OTPRateLimit limits OTP requests to 5 per phone number per 10 minutes.
// The phone number is read from the JSON request body field "phone".
// If rdb is nil, rate limiting is skipped (useful in tests).
func OTPRateLimit(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}
		// Read body bytes, re-set body for downstream handlers
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var req struct {
			Phone string `json:"phone"`
		}
		json.Unmarshal(bodyBytes, &req)
		// Re-set body again for downstream handler
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		if req.Phone != "" {
			key := fmt.Sprintf("otp_rl:%s", req.Phone)
			if !allow(c.Request.Context(), rdb, key, 5, 600*time.Second) {
				c.Header("Retry-After", "600")
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": gin.H{"code": "RATE_LIMITED", "message": "Too many OTP requests. Try again later."},
				})
				return
			}
		}
		c.Next()
	}
}

// PasswordResetRateLimit caps `/forgot-password` to 3 attempts per
// identifier (email or phone) per hour and 10 per IP per hour. Audit
// A12: previously `/forgot-password` was an open public POST with no
// gate — an attacker could spam SMS/email to any target until the
// provider blocked the account, locking the victim out. Identifier is
// read from the JSON body (phone or email). Reset tokens themselves
// remain server-issued and short-lived; this is the upstream throttle.
func PasswordResetRateLimit(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var req struct {
			Email string `json:"email"`
			Phone string `json:"phone"`
		}
		json.Unmarshal(bodyBytes, &req)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		identifier := req.Email
		if identifier == "" {
			identifier = req.Phone
		}
		ip := c.ClientIP()

		// 3 per identifier per hour
		if identifier != "" {
			if !allow(c.Request.Context(), rdb, fmt.Sprintf("pwreset_rl:id:%s", identifier), 3, time.Hour) {
				c.Header("Retry-After", "3600")
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": gin.H{"code": "RATE_LIMITED", "message": "Too many password reset requests for this account. Try again later."},
				})
				return
			}
		}
		// 10 per IP per hour
		if !allow(c.Request.Context(), rdb, fmt.Sprintf("pwreset_rl:ip:%s", ip), 10, time.Hour) {
			c.Header("Retry-After", "3600")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "RATE_LIMITED", "message": "Too many password reset requests from this IP. Try again later."},
			})
			return
		}
		c.Next()
	}
}

// LoginRateLimit limits login attempts: 10 per IP per 15 min AND 5 per identifier per 15 min.
// If rdb is nil, rate limiting is skipped (useful in tests).
func LoginRateLimit(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rdb == nil {
			c.Next()
			return
		}
		bodyBytes, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var req struct {
			Identifier string `json:"identifier"` // phone or email
			Phone      string `json:"phone"`
		}
		json.Unmarshal(bodyBytes, &req)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		ip := c.ClientIP()
		ipKey := fmt.Sprintf("login_rl_ip:%s", ip)
		if !allow(c.Request.Context(), rdb, ipKey, 10, 900*time.Second) {
			c.Header("Retry-After", "900")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "RATE_LIMITED", "message": "Too many login attempts. Try again later."},
			})
			return
		}

		identifier := req.Identifier
		if identifier == "" {
			identifier = req.Phone
		}
		if identifier != "" {
			idKey := fmt.Sprintf("login_rl_id:%s", identifier)
			if !allow(c.Request.Context(), rdb, idKey, 5, 900*time.Second) {
				c.Header("Retry-After", "900")
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": gin.H{"code": "RATE_LIMITED", "message": "Too many login attempts. Try again later."},
				})
				return
			}
		}
		c.Next()
	}
}

// allow returns true if the action is within rate limit using Redis INCR + EXPIRE.
//
// Audit A2: previously returned `true` on Redis error (`count, _ := incr.Result()`
// silently dropped the error; on outage count was 0, which <= every limit).
// That meant a Redis blip disabled every brute-force gate — login, OTP,
// password-reset — all at once. Now fails CLOSED: any Redis error is
// logged at WARN and the request is denied. Matches the fail-closed
// fix already shipped in notification-service HS5 and call-service C4.
func allow(ctx context.Context, rdb *redis.Client, key string, limit int64, window time.Duration) bool {
	pipe := rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("auth rate limiter Redis error — failing closed", "key", key, "err", err)
		return false
	}
	count, err := incr.Result()
	if err != nil {
		slog.Warn("auth rate limiter INCR result error — failing closed", "key", key, "err", err)
		return false
	}
	return count <= limit
}
