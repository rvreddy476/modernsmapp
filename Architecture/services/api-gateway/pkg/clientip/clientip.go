// Package clientip decides which address a request came from.
//
// Audit M1: the per-IP rate limit keyed on X-Real-IP, or else the LEFTMOST
// X-Forwarded-For entry. Both are whatever the client wrote: the AWS ALB
// appends the peer address to X-Forwarded-For and does not set X-Real-IP. A
// client sending a fresh X-Real-IP per request got a fresh bucket every time,
// which bypassed the only edge throttle on /v1/auth/* (login, OTP, 2FA,
// reset) and grew the in-memory limiter without bound.
//
// The client address is now taken from the TCP peer and walked back through
// X-Forwarded-For ONLY across proxies the deployment says it trusts:
//
//   - GATEWAY_TRUSTED_PROXY_CIDRS (AWS: the VPC CIDR the ALB's ENIs sit in).
//     When the peer is inside one of these ranges, X-Forwarded-For is read
//     right to left and the first address outside them is the client. An
//     entry the client wrote further left is never reached.
//   - GATEWAY_TRUSTED_PROXY_HOPS (e.g. 1 behind a single ingress-nginx whose
//     address is not known in advance). The client is the Nth entry from the
//     right; a header with fewer entries did not pass through the proxies and
//     the peer is used.
//
// Neither set means no proxy is trusted: the peer is the client, and every
// forwarding header is ignored. X-Real-IP is never read.
//
// Middleware stores the result in the request context and rewrites
// X-Forwarded-For and X-Real-IP to it, so upstreams that read those headers
// (gin's ClientIP, auth-service's login limiter) see the same address instead
// of the client's forgery.
//
// Lives under pkg/ because a new file under cmd/server/ is git-ignored.
package clientip

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Environment variables read by FromEnv.
const (
	EnvTrustedCIDRs = "GATEWAY_TRUSTED_PROXY_CIDRS"
	EnvTrustedHops  = "GATEWAY_TRUSTED_PROXY_HOPS"
)

// maxHops bounds GATEWAY_TRUSTED_PROXY_HOPS; no deployment has more than a
// couple of proxies in front of the gateway.
const maxHops = 10

// Resolver picks the client address. The zero value trusts no proxy.
type Resolver struct {
	trusted []*net.IPNet
	hops    int
}

// New builds a resolver. Use either cidrs or hops, not both.
func New(cidrs []string, hops int) (Resolver, error) {
	if len(cidrs) > 0 && hops > 0 {
		return Resolver{}, errors.New("set either trusted proxy CIDRs or a trusted hop count, not both")
	}
	if hops < 0 || hops > maxHops {
		return Resolver{}, fmt.Errorf("trusted proxy hops must be between 0 and %d", maxHops)
	}
	r := Resolver{hops: hops}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return Resolver{}, fmt.Errorf("invalid trusted proxy CIDR %q: %w", c, err)
		}
		r.trusted = append(r.trusted, n)
	}
	return r, nil
}

// FromEnv reads GATEWAY_TRUSTED_PROXY_CIDRS (comma separated) and
// GATEWAY_TRUSTED_PROXY_HOPS. An invalid value is an error: the caller
// refuses to start rather than guess.
func FromEnv(getenv func(string) string) (Resolver, error) {
	var cidrs []string
	if raw := strings.TrimSpace(getenv(EnvTrustedCIDRs)); raw != "" {
		cidrs = strings.Split(raw, ",")
	}
	hops := 0
	if raw := strings.TrimSpace(getenv(EnvTrustedHops)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Resolver{}, fmt.Errorf("%s=%q is not a number", EnvTrustedHops, raw)
		}
		hops = n
	}
	return New(cidrs, hops)
}

// TrustsProxies reports whether any forwarding header is honoured.
func (r Resolver) TrustsProxies() bool { return len(r.trusted) > 0 || r.hops > 0 }

func (r Resolver) isTrusted(ip net.IP) bool {
	for _, n := range r.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve returns the client address for req, without a port.
func (r Resolver) Resolve(req *http.Request) string {
	peer := PeerIP(req)
	if !r.TrustsProxies() {
		return peer
	}
	xff := forwardedFor(req)
	if r.hops > 0 {
		if len(xff) < r.hops {
			return peer
		}
		if ip := net.ParseIP(xff[len(xff)-r.hops]); ip != nil {
			return ip.String()
		}
		return peer
	}
	peerIP := net.ParseIP(peer)
	if peerIP == nil || !r.isTrusted(peerIP) {
		return peer
	}
	// Right to left: each entry was appended by the hop to its right. Stop at
	// the first address no trusted proxy accounts for.
	client := peer
	for i := len(xff) - 1; i >= 0; i-- {
		ip := net.ParseIP(xff[i])
		if ip == nil {
			// Garbage a trusted proxy would never write: the hop that
			// received it is as far back as the chain can be believed.
			return client
		}
		client = ip.String()
		if !r.isTrusted(ip) {
			return client
		}
	}
	return client
}

// forwardedFor splits every X-Forwarded-For header into trimmed entries, in
// order (several header lines count as one comma-separated list).
func forwardedFor(req *http.Request) []string {
	var out []string
	for _, line := range req.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(line, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// PeerIP is the TCP peer address without its port.
func PeerIP(req *http.Request) string {
	if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return host
	}
	return req.RemoteAddr
}

type ctxKey struct{}

type resolved struct {
	ip        string
	forwarded bool
}

// forwardingHeaders are set by any HTTP proxy in front of the gateway. The
// ALB always sets X-Forwarded-For and X-Forwarded-Proto; ingress-nginx sets
// both and X-Real-IP.
var forwardingHeaders = []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-IP", "Forwarded"}

func hasForwardingHeader(req *http.Request) bool {
	for _, h := range forwardingHeaders {
		if len(req.Header.Values(h)) > 0 {
			return true
		}
	}
	return false
}

// FromRequest returns the address Middleware resolved, or the TCP peer when
// the middleware did not run. It never reads a header.
func FromRequest(req *http.Request) string {
	if v, ok := req.Context().Value(ctxKey{}).(resolved); ok && v.ip != "" {
		return v.ip
	}
	return PeerIP(req)
}

// ArrivedForwarded reports whether the request reached the gateway carrying
// any proxy forwarding header, i.e. it came through the load balancer rather
// than straight from inside the cluster. It answers for the request as it
// arrived, before Middleware rewrote the headers.
func ArrivedForwarded(req *http.Request) bool {
	if v, ok := req.Context().Value(ctxKey{}).(resolved); ok {
		return v.forwarded
	}
	return hasForwardingHeader(req)
}

// Middleware resolves the client address once, records it in the context and
// replaces the forwarding headers with it before anything downstream reads
// them. httputil.ReverseProxy then appends the peer, so an upstream sees
// "client, proxy".
func Middleware(r Resolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		v := resolved{ip: r.Resolve(req), forwarded: hasForwardingHeader(req)}
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Real-IP")
		req.Header.Del("Forwarded")
		if v.ip != "" {
			req.Header.Set("X-Forwarded-For", v.ip)
			req.Header.Set("X-Real-IP", v.ip)
		}
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, v)))
	})
}
