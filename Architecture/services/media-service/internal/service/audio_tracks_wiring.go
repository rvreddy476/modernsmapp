package service

import (
	"context"
	"log/slog"

	"github.com/atpost/media-service/internal/dubbing"
)

// AudioTracks exposes the alternate-audio-track feature to the HTTP layer.
func (s *Service) AudioTracks() *AudioTracks { return s.audioTracks }

// WithDubber wires the generation backend (main.go, from dubbing.Select).
func (s *Service) WithDubber(d dubbing.Dubber) *Service {
	s.audioTracks.WithDubber(d)
	return s
}

// StartAudioTrackWorker drains media_audio_tracks in this process (the
// server image has ffmpeg). Runs whether or not a dubber is configured:
// uploaded tracks need no AI, only the mux.
func (s *Service) StartAudioTrackWorker(ctx context.Context) {
	if s.audioTracks == nil {
		return
	}
	if s.audioTracks.DubbingConfigured() {
		slog.Info("audio tracks: worker started", "dubber", s.audioTracks.dubber.Name())
	} else {
		slog.Info("audio tracks: worker started (uploads only; no dubbing backend configured)")
	}
	s.audioTracks.StartWorker(ctx)
}
