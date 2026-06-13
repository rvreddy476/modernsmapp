package http

import (
	"bufio"
	"log/slog"
	"net"
	nethttp "net/http"
	"strings"
	"time"
)

func loggingMiddleware(log *slog.Logger, trustedProxies []string, next nethttp.Handler) nethttp.Handler {
	if log == nil {
		log = slog.Default()
	}
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: nethttp.StatusOK}
		next.ServeHTTP(rec, r)

		if strings.HasSuffix(r.URL.Path, "/health") {
			return
		}
		duration := time.Since(start)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", duration.Milliseconds(),
			"client_ip", readClientIP(r, trustedProxies),
		}
		switch {
		case rec.status >= nethttp.StatusInternalServerError:
			log.Error("request completed", attrs...)
		case rec.status >= nethttp.StatusBadRequest:
			log.Warn("request completed", attrs...)
		default:
			log.Info("request completed", attrs...)
		}
	})
}

type statusRecorder struct {
	nethttp.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(nethttp.Hijacker)
	if !ok {
		return nil, nil, nethttp.ErrNotSupported
	}
	return hj.Hijack()
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(nethttp.Flusher); ok {
		f.Flush()
	}
}
