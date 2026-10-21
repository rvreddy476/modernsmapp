package server

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Default timeouts every service gets unless its Config overrides them.
const (
	DefaultReadTimeout       = 15 * time.Second
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 90 * time.Second
)

type Config struct {
	Port            string
	ShutdownTimeout time.Duration
	OnShutdown      func()

	// ReadTimeout and WriteTimeout override DefaultReadTimeout and
	// DefaultWriteTimeout when > 0. Zero keeps the default, so every
	// existing caller is unchanged. Only a service that legitimately takes
	// large, slow request bodies sets them — media-service, whose resumable
	// upload chunks are up to 64 MiB (≈ 55 s at 10 Mbps) and whose /complete
	// assembles thousands of parts. ReadHeaderTimeout is never overridden,
	// so a slow-header (slowloris) client is still cut off at 5 s.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// newHTTPServer builds the http.Server Run serves, with cfg's overrides
// applied over the defaults. cfg.Port must already be defaulted.
func newHTTPServer(handler http.Handler, cfg Config) *http.Server {
	readTimeout := DefaultReadTimeout
	if cfg.ReadTimeout > 0 {
		readTimeout = cfg.ReadTimeout
	}
	writeTimeout := DefaultWriteTimeout
	if cfg.WriteTimeout > 0 {
		writeTimeout = cfg.WriteTimeout
	}
	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: DefaultReadHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       DefaultIdleTimeout,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}
}

func Run(handler http.Handler, cfg Config) error {
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}

	srv := newHTTPServer(handler, cfg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if cfg.OnShutdown != nil {
			cfg.OnShutdown()
		}

		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
		return nil
	}
}
