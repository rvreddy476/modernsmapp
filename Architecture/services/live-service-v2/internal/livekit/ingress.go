package livekit

// LiveKit Ingress (1 Oct 2026): a host goes live from OBS, a hardware encoder
// or a camera by pushing RTMP to an ingress that publishes into the stream's
// room.
//
// Checked against github.com/livekit/protocol v1.45.5
// (protobufs/livekit_ingress.proto, auth/grants.go) and server-sdk-go
// v2.16.2 (ingressclient.go):
//
//   - service livekit.Ingress, twirp prefix /twirp/livekit.Ingress/, rpcs
//     CreateIngress(CreateIngressRequest) IngressInfo,
//     ListIngress(ListIngressRequest) ListIngressResponse,
//     DeleteIngress(DeleteIngressRequest) IngressInfo;
//   - CreateIngressRequest: input_type (enum IngressInput, RTMP_INPUT = 0),
//     name, room_name, participant_identity, participant_name;
//   - IngressInfo: ingress_id, stream_key, url ("URL to point the encoder to
//     for push (RTMP, WHIP)");
//   - ListIngressRequest: ingress_id (optional filter); ListIngressResponse:
//     items (repeated IngressInfo);
//   - DeleteIngressRequest: ingress_id;
//   - every call is signed with the video grant {"ingressAdmin": true}
//     (VideoGrant.IngressAdmin, json "ingressAdmin"; the SDK signs
//     withVideoGrant{IngressAdmin: true} for all four rpcs).
//
// The twirp server marshals with UseProtoNames unless it was built with
// jsonCamelCase, so responses are snake_case; the decoder below takes the
// lowerCamelCase names too.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// IngressRequest is what CreateRTMPIngress needs.
type IngressRequest struct {
	// Name labels the ingress in LiveKit's own listings.
	Name string
	// Room is the room the ingress publishes into.
	Room string
	// Identity is the participant identity the ingress joins as.
	Identity string
	// ParticipantName is that participant's display name.
	ParticipantName string
}

// Ingress is the slice of LiveKit's IngressInfo the service hands to the
// host: where the encoder pushes, and the key it pushes with.
type Ingress struct {
	ID        string
	URL       string
	StreamKey string
}

// redactedKey stands in for the stream key wherever an Ingress is printed.
const redactedKey = "[redacted]"

// String never prints the stream key (fmt's %v, %+v and %s).
func (i Ingress) String() string {
	return fmt.Sprintf("Ingress{ID:%s URL:%s StreamKey:%s}", i.ID, i.URL, redactedKey)
}

// GoString never prints the stream key (fmt's %#v).
func (i Ingress) GoString() string { return i.String() }

// LogValue never logs the stream key (log/slog).
func (i Ingress) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", i.ID), slog.String("url", i.URL), slog.String("stream_key", redactedKey))
}

// ingressGrant is the video grant LiveKit's ingress service requires.
func ingressGrant() map[string]any { return map[string]any{"ingressAdmin": true} }

// ingressInfo decodes an IngressInfo in proto names or lowerCamelCase.
type ingressInfo struct {
	IngressID      string `json:"ingress_id"`
	IngressIDCamel string `json:"ingressId"`
	StreamKey      string `json:"stream_key"`
	StreamKeyCamel string `json:"streamKey"`
	URL            string `json:"url"`
}

func (i ingressInfo) ingress() *Ingress {
	out := &Ingress{ID: i.IngressID, URL: i.URL, StreamKey: i.StreamKey}
	if out.ID == "" {
		out.ID = i.IngressIDCamel
	}
	if out.StreamKey == "" {
		out.StreamKey = i.StreamKeyCamel
	}
	return out
}

// errIngressIncomplete: LiveKit answered without an id, a URL or a key. The
// answer is never echoed (it may hold a key).
var errIngressIncomplete = errors.New("livekit: ingress answer is missing its id, url or stream key")

func (c *httpClient) CreateRTMPIngress(ctx context.Context, req IngressRequest) (*Ingress, error) {
	if err := c.requireConfigured(); err != nil {
		return nil, err
	}
	if req.Room == "" || req.Identity == "" {
		return nil, fmt.Errorf("livekit: ingress needs a room and a participant identity")
	}
	body := map[string]any{
		"input_type":           "RTMP_INPUT",
		"name":                 req.Name,
		"room_name":            req.Room,
		"participant_identity": req.Identity,
		"participant_name":     req.ParticipantName,
	}
	var info ingressInfo
	if err := c.twirpCallGrant(ctx, "/twirp/livekit.Ingress/CreateIngress", ingressGrant(), body, &info); err != nil {
		return nil, err
	}
	ing := info.ingress()
	if ing.ID == "" || ing.URL == "" || ing.StreamKey == "" {
		return nil, errIngressIncomplete
	}
	return ing, nil
}

func (c *httpClient) GetIngress(ctx context.Context, ingressID string) (*Ingress, error) {
	if err := c.requireConfigured(); err != nil {
		return nil, err
	}
	if ingressID == "" {
		return nil, nil
	}
	var resp struct {
		Items []ingressInfo `json:"items"`
	}
	err := c.twirpCallGrant(ctx, "/twirp/livekit.Ingress/ListIngress", ingressGrant(),
		map[string]any{"ingress_id": ingressID}, &resp)
	// twirpCallGrant reports any twirp not_found as ErrRoomNotFound.
	if errors.Is(err, ErrRoomNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, item := range resp.Items {
		ing := item.ingress()
		// The filter is LiveKit's; never hand back a different ingress.
		if ing.ID != ingressID {
			continue
		}
		if ing.URL == "" || ing.StreamKey == "" {
			return nil, errIngressIncomplete
		}
		return ing, nil
	}
	return nil, nil
}

func (c *httpClient) DeleteIngress(ctx context.Context, ingressID string) error {
	if err := c.requireConfigured(); err != nil {
		return err
	}
	if ingressID == "" {
		return nil
	}
	err := c.twirpCallGrant(ctx, "/twirp/livekit.Ingress/DeleteIngress", ingressGrant(),
		map[string]any{"ingress_id": ingressID}, nil)
	if errors.Is(err, ErrRoomNotFound) {
		return nil
	}
	return err
}
