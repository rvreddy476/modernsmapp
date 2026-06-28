//go:build integration

package integration

import "testing"

// P2 breadth — live streaming. Read smokes for both the v1 and v2 live services
// (listing active streams). The full create→ingest(RTMP)→viewer-join→end journey
// needs a media ingest endpoint (mediamtx/LiveKit) and is a deeper follow-up.

func TestE2E_Live_Streams(t *testing.T)   { get200(t, "/v1/live/streams") }
func TestE2E_LiveV2_Streams(t *testing.T) { get200(t, "/v1/livestream/streams") }
