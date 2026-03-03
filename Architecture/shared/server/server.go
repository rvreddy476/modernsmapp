package server

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Config struct {
	Port            string
	ShutdownTimeout time.Duration
	OnShutdown      func()
}

func Run(handler http.Handler, cfg Config) error {
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
	}

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
