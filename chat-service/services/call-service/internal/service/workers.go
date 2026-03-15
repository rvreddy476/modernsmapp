package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/store/postgres"
)

// RingingTimeoutWorker expires calls that have been ringing too long.
type RingingTimeoutWorker struct {
	store          *postgres.CallStore
	svc            *Service
	log            *slog.Logger
	timeoutSeconds int
}

func NewRingingTimeoutWorker(store *postgres.CallStore, svc *Service, log *slog.Logger, timeoutSeconds int) *RingingTimeoutWorker {
	return &RingingTimeoutWorker{
		store:          store,
		svc:            svc,
		log:            log,
		timeoutSeconds: timeoutSeconds,
	}
}

func (w *RingingTimeoutWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkTimeouts(ctx)
		}
	}
}

func (w *RingingTimeoutWorker) checkTimeouts(ctx context.Context) {
	cutoff := time.Now().Add(-time.Duration(w.timeoutSeconds) * time.Second)
	sessions, err := w.store.GetRingingCallsOlderThan(ctx, cutoff)
	if err != nil {
		w.log.Warn("failed to query ringing calls", "err", err)
		return
	}

	for _, cs := range sessions {
		w.log.Info("expiring ringing call due to timeout",
			"call_id", cs.ID,
			"created_at", cs.CreatedAt,
			"timeout_seconds", w.timeoutSeconds,
		)
		w.svc.endCallInternal(ctx, cs.ID, cs.InitiatorUserID, domain.EndedReasonMissed)
	}
}
