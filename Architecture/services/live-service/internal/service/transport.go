package service

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrPlaybackUnavailable      = errors.New("playback unavailable")
	ErrPlaybackNotConfigured    = errors.New("playback origin is not configured")
	ErrInvalidPlaybackAssetPath = errors.New("invalid playback asset path")
	ErrPublishUnavailable       = errors.New("publish unavailable")
	ErrPublishNotConfigured     = errors.New("publish origin is not configured")
)

type StreamMediaConfig struct {
	PlaybackURLTemplate     string
	PlaybackBaseURL         string
	PlaybackInternalBaseURL string
	PlaybackProtocol        string
	PublishURLTemplate      string
	PublishInternalBaseURL  string
	PublishProtocol         string
	IngestURL               string
	IngestProtocol          string
}

func (c StreamMediaConfig) withDefaults() StreamMediaConfig {
	cfg := c
	if strings.TrimSpace(cfg.PlaybackProtocol) == "" {
		cfg.PlaybackProtocol = "hls"
	}
	if strings.TrimSpace(cfg.PublishProtocol) == "" {
		cfg.PublishProtocol = "whip"
	}
	if strings.TrimSpace(cfg.IngestProtocol) == "" {
		cfg.IngestProtocol = "rtmp"
	}
	return cfg
}

func (c StreamMediaConfig) playbackURL(stream *postgres.Stream) *string {
	if stream == nil || stream.Status != "live" {
		return nil
	}

	var raw string
	switch {
	case strings.TrimSpace(c.PlaybackURLTemplate) != "":
		raw = strings.NewReplacer(
			"{stream_id}", stream.ID.String(),
			"{host_id}", stream.HostID.String(),
			"{stream_key}", stream.StreamKey,
		).Replace(strings.TrimSpace(c.PlaybackURLTemplate))
	case strings.TrimSpace(c.PlaybackBaseURL) != "":
		raw = strings.TrimRight(strings.TrimSpace(c.PlaybackBaseURL), "/") + "/" + stream.ID.String() + "/index.m3u8"
	default:
		return nil
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return &raw
}

func (c StreamMediaConfig) playbackInternalAssetURL(stream *postgres.Stream, assetPath string) (*url.URL, error) {
	if stream == nil {
		return nil, postgres.ErrNotFound
	}
	if stream.Status != "live" {
		return nil, ErrPlaybackUnavailable
	}
	if strings.TrimSpace(stream.StreamKey) == "" {
		return nil, ErrPlaybackUnavailable
	}

	base := strings.TrimRight(strings.TrimSpace(c.PlaybackInternalBaseURL), "/")
	if base == "" {
		return nil, ErrPlaybackNotConfigured
	}

	asset := strings.ReplaceAll(strings.TrimSpace(assetPath), "\\", "/")
	asset = strings.TrimLeft(asset, "/")
	if asset == "" {
		asset = "index.m3u8"
	}
	for _, segment := range strings.Split(asset, "/") {
		if segment == ".." {
			return nil, ErrInvalidPlaybackAssetPath
		}
	}
	asset = strings.TrimPrefix(path.Clean("/"+asset), "/")
	if asset == "" || asset == "." {
		asset = "index.m3u8"
	}

	streamPath := c.streamPath(stream)

	return url.Parse(base + "/" + streamPath + "/" + asset)
}

func (c StreamMediaConfig) publishInternalEndpointURL(stream *postgres.Stream) (*url.URL, error) {
	if stream == nil {
		return nil, postgres.ErrNotFound
	}
	if stream.Status == "ended" {
		return nil, ErrPublishUnavailable
	}
	if strings.TrimSpace(stream.StreamKey) == "" {
		return nil, ErrPublishUnavailable
	}

	base := strings.TrimRight(strings.TrimSpace(c.PublishInternalBaseURL), "/")
	if base == "" {
		return nil, ErrPublishNotConfigured
	}

	return url.Parse(base + "/" + c.streamPath(stream) + "/whip")
}

func (c StreamMediaConfig) publishURL(stream *postgres.Stream) *string {
	if stream == nil || stream.Status == "ended" || strings.TrimSpace(stream.StreamKey) == "" {
		return nil
	}

	raw := strings.TrimSpace(c.PublishURLTemplate)
	if raw == "" {
		return nil
	}
	raw = strings.NewReplacer(
		"{stream_id}", stream.ID.String(),
		"{host_id}", stream.HostID.String(),
		"{stream_key}", stream.StreamKey,
	).Replace(raw)
	if raw == "" {
		return nil
	}
	return &raw
}

func (c StreamMediaConfig) ingestURL(stream *postgres.Stream) *string {
	if stream == nil || stream.Status == "ended" || strings.TrimSpace(stream.StreamKey) == "" {
		return nil
	}

	raw := strings.TrimSpace(c.IngestURL)
	if raw == "" {
		return nil
	}
	return &raw
}

func (c StreamMediaConfig) streamPath(stream *postgres.Stream) string {
	rtmpApp := ""
	if ingestRaw := strings.TrimSpace(c.IngestURL); ingestRaw != "" {
		if u, err := url.Parse(ingestRaw); err == nil {
			rtmpApp = strings.Trim(u.Path, "/")
		}
	}

	streamPath := stream.StreamKey
	if rtmpApp != "" {
		streamPath = rtmpApp + "/" + stream.StreamKey
	}
	return streamPath
}

func (s *Service) decorateStream(stream *postgres.Stream, includeIngest bool) *postgres.Stream {
	if stream == nil {
		return nil
	}

	cfg := s.mediaConfig.withDefaults()
	cloned := *stream
	cloned.PlaybackURL = cfg.playbackURL(&cloned)
	if cloned.PlaybackURL != nil {
		protocol := cfg.PlaybackProtocol
		cloned.PlaybackProtocol = &protocol
	} else {
		cloned.PlaybackProtocol = nil
	}

	if includeIngest {
		cloned.IngestURL = cfg.ingestURL(&cloned)
		if cloned.IngestURL != nil {
			protocol := cfg.IngestProtocol
			cloned.IngestProtocol = &protocol
		} else {
			cloned.IngestProtocol = nil
		}
		cloned.PublishURL = cfg.publishURL(&cloned)
		if cloned.PublishURL != nil {
			protocol := cfg.PublishProtocol
			cloned.PublishProtocol = &protocol
		} else {
			cloned.PublishProtocol = nil
		}
	} else {
		cloned.IngestURL = nil
		cloned.IngestProtocol = nil
		cloned.PublishURL = nil
		cloned.PublishProtocol = nil
	}

	return &cloned
}

func (s *Service) decorateStreams(streams []postgres.Stream, includeIngest bool) []postgres.Stream {
	if len(streams) == 0 {
		return streams
	}

	decorated := make([]postgres.Stream, 0, len(streams))
	for i := range streams {
		st := s.decorateStream(&streams[i], includeIngest)
		if st != nil {
			decorated = append(decorated, *st)
		}
	}
	return decorated
}

func (s *Service) ResolvePlaybackAssetURL(ctx context.Context, streamID uuid.UUID, assetPath string) (*url.URL, error) {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return nil, err
	}
	return s.mediaConfig.withDefaults().playbackInternalAssetURL(st, assetPath)
}

func (s *Service) ResolvePublishEndpointURL(ctx context.Context, streamID, hostID uuid.UUID) (*url.URL, error) {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return nil, err
	}
	if st.HostID != hostID {
		return nil, postgres.ErrNotFound
	}
	return s.mediaConfig.withDefaults().publishInternalEndpointURL(st)
}
