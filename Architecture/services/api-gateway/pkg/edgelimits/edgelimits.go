// Package edgelimits holds the gateway's transport limits: the http.Server
// timeouts and the request-body cap.
//
// Security sweep #18 (21 Oct 2026): the gateway served with
// http.ListenAndServe — no ReadHeaderTimeout, no IdleTimeout, no header cap —
// and put no bound on a request body. A slowloris client could hold
// connections open indefinitely, and an unbounded JSON body went straight to
// the gin upstreams, most of which decode without a limit of their own.
//
// What is deliberately NOT set: ReadTimeout and WriteTimeout. Both cover the
// whole body / whole response, so they would cut off a 64 MiB resumable chunk
// on a slow phone link, every SSE stream, every websocket and every HLS
// segment being proxied. ReadHeaderTimeout bounds the slowloris phase (the
// request line and headers); the body is bounded in size instead of time.
//
// The body cap is per prefix: upload routes (media chunks and multipart
// uploads, voiceovers, subtitle files, post uploads) are exempt because the
// upstream bounds them itself (resumable.go maxChunkSize, audio_tracks.go
// MaxAudioTrackUploadBytes); everything else is capped at MaxBodyBytes. The
// package lives under pkg/ so it is tracked (the root .gitignore `server`
// rule has swallowed new cmd/server/ files before).
package edgelimits

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// ReadHeaderTimeout bounds the request line and headers.
	ReadHeaderTimeout = 10 * time.Second
	// IdleTimeout closes a keep-alive connection that carries no request.
	IdleTimeout = 120 * time.Second
	// MaxHeaderBytes caps the request line plus headers (the net/http
	// default, made explicit so a future change is a visible decision).
	MaxHeaderBytes = 1 << 20

	// MaxBodyBytes caps a request body on every non-upload route. The
	// largest legitimate non-upload body is a long post or a batch request,
	// both far below this; the services that read raw bodies cap themselves
	// at 64 KiB to 1 MiB.
	MaxBodyBytes int64 = 10 << 20

	// Code is the error code of a refused body.
	Code = "PAYLOAD_TOO_LARGE"
)

// UploadPrefixes are the route prefixes whose bodies are file bytes and are
// bounded by their upstream, so the edge cap does not apply. Matched on a
// path-segment boundary: /v1/audio does not exempt /v1/audio-library.
var UploadPrefixes = []string{
	"/v1/media",     // media-service: resumable chunks (≤64 MiB), audio tracks (multipart, ≤50 MiB)
	"/v1/audio",     // media-service: POST /v1/audio/voiceover (multipart)
	"/v1/subtitles", // media-service: owner uploads a subtitle track
	"/v1/uploads",   // post-service uploads
}

// body is the exact 413 response.
const body = `{"error":{"code":"` + Code + `","message":"Request body is too large"}}`

// NewServer is the gateway's http.Server: the timeouts above, no
// ReadTimeout/WriteTimeout (see the package comment).
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: ReadHeaderTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
	}
}

// Exempt reports whether path is an upload route the body cap skips.
func Exempt(path string) bool {
	for _, prefix := range UploadPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

type exceededKey struct{}

// LimitBody caps the request body of every non-upload route at max bytes.
// A declared Content-Length over the cap is refused at once with 413. A body
// of unknown length (chunked) is wrapped in http.MaxBytesReader; when the
// upstream read trips it, the proxy error handler answers 413 instead of 502
// (ProxyErrorHandler).
func LimitBody(max int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody || Exempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > max {
			WriteTooLarge(w)
			return
		}
		exceeded := new(atomic.Bool)
		r = r.WithContext(context.WithValue(r.Context(), exceededKey{}, exceeded))
		r.Body = &flaggingBody{ReadCloser: http.MaxBytesReader(w, r.Body, max), exceeded: exceeded}
		next.ServeHTTP(w, r)
	})
}

// Exceeded reports whether the body of the request behind ctx went over the
// cap LimitBody set.
func Exceeded(ctx context.Context) bool {
	flag, _ := ctx.Value(exceededKey{}).(*atomic.Bool)
	return flag != nil && flag.Load()
}

// WriteTooLarge writes the 413 response.
func WriteTooLarge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_, _ = w.Write([]byte(body))
}

// ProxyErrorHandler is httputil.ReverseProxy.ErrorHandler: 413 when the
// request failed because its body went over the cap, otherwise what the
// default handler does (log, 502).
func ProxyErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if Exceeded(r.Context()) {
		WriteTooLarge(w)
		return
	}
	log.Printf("http: proxy error: %v", err)
	w.WriteHeader(http.StatusBadGateway)
}

// flaggingBody records that MaxBytesReader refused the body, so the proxy's
// error handler can tell an oversized body from an upstream failure.
type flaggingBody struct {
	io.ReadCloser
	exceeded *atomic.Bool
}

func (b *flaggingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var tooLarge *http.MaxBytesError
	if err != nil && errors.As(err, &tooLarge) {
		b.exceeded.Store(true)
	}
	return n, err
}
