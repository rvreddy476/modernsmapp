package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HEAD on the serve routes (channel RSS feeds, 2026-09-29).
//
// WHY THIS SERVICE ANSWERS HEAD ITSELF
//
// Feed validators and podcast apps HEAD an enclosure before they list or
// download it. GET /v1/media/:id/serve[/:variant] answers 307 to a signed
// object URL, and that signature is for GET: a HEAD that follows the
// redirect is refused by S3/MinIO (and by CloudFront's signed behaviour), so
// the enclosure looks broken to exactly the clients a feed exists for. The
// length and type they ask for are already in this service's own rows, so
// the HEAD is answered here, from metadata, with no redirect and no object
// read.
//
// THE DECISION IS THE GET'S
//
// HeadForViewer walks the same steps Service.GetMediaVariantURL and
// Service.StreamAnonymous walk, through the same functions:
//
//   - DatingScopeDenies — a dating photo is its uploader's alone;
//   - an anonymous-scoped asset: authorizeCaptionRead, the call
//     StreamAnonymous makes (uploader short-circuit, moderation, then the
//     content authorities);
//   - every other asset: delivery.Gate.URLFor / URLForVariant over the very
//     object key the GET would redirect to. The URL is discarded. Calling
//     the signing entry point rather than a second "authorize only" path is
//     deliberate: there is one implementation of the class rule and of the
//     fail-closed behaviour, and a 200 here means the GET would have
//     produced its redirect — signer included.
//
// Nothing here decides an audience on its own.

// ServeHead is what a HEAD on a serve route answers with.
type ServeHead struct {
	ContentType string
	// Size is the object's length in bytes, or -1 when no length was ever
	// recorded for it (a row written before sizes were stored). The handler
	// then omits Content-Length: an absent length is honest, a zero is not.
	Size int64
}

// serveHeadStore is the store slice the HEAD needs. An interface so the
// handler tests drive every status path against the real decision without
// PostgreSQL.
type serveHeadStore interface {
	GetMediaWithVariants(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
}

// ServeHeads answers HEAD on the serve routes. It holds no object store on
// purpose: there is nothing it could open.
type ServeHeads struct {
	store serveHeadStore
	gate  *delivery.Gate
}

// NewServeHeads builds the reads over explicit dependencies. The handler
// tests use it with fakes; production goes through Service.ServeHeads.
func NewServeHeads(store serveHeadStore, gate *delivery.Gate) *ServeHeads {
	return &ServeHeads{store: store, gate: gate}
}

// ServeHeads exposes the HEAD reads over the service's own store and gate.
// Built per call so a gate wired after New (WithDeliveryGate, main.go) is the
// one used.
func (s *Service) ServeHeads() *ServeHeads {
	return NewServeHeads(s.pgStore, s.gate)
}

// servedObject is the object a serve route names, with what the rows say
// about it.
type servedObject struct {
	// name is the variant that was resolved ("original" for the uploaded
	// file), which for the avatar alias is not the name that was asked for.
	name string
	key  string
	mime string
	size int64
}

func originalObject(media *postgres.MediaAsset) servedObject {
	size := int64(-1)
	if media.FileSizeBytes > 0 {
		size = media.FileSizeBytes
	}
	return servedObject{name: "original", key: media.StorageKey, mime: media.MimeType, size: size}
}

func variantObject(media *postgres.MediaAsset, v postgres.MediaVariant) servedObject {
	size := int64(-1)
	if v.SizeBytes != nil && *v.SizeBytes > 0 {
		size = *v.SizeBytes
	}
	mime := v.Mime
	if mime == "" {
		// The same fallback StreamAnonymous applies to a variant row that
		// recorded no type.
		mime = media.MimeType
	}
	return servedObject{name: v.Name, key: v.ObjectKey, mime: mime, size: size}
}

// resolveServedObject picks the object `/serve/<variant>` names, from the
// rows alone. ok is false when the asset has no such rendition — which is
// also the answer for "hls": the master playlist is rewritten per request
// and has no row, so there is no length to report.
//
// avatarLadder is whether the avatar alias applies. It does on the redirect
// path (GetMediaVariantURL) and does not for an anonymous-scoped asset, whose
// GET looks every name up literally (StreamAnonymous); the HEAD must agree
// with the GET it stands in for.
func resolveServedObject(media *postgres.MediaAsset, variant string, avatarLadder bool) (servedObject, bool) {
	if variant == "original" {
		return originalObject(media), true
	}
	if avatarLadder && variant == AvatarVariant {
		// avatarRenditionLadder is the one ladder; pickAvatarRendition walks
		// it for the GET. TestHeadAvatarResolvesTheKeyTheGetDelivers pins
		// that the two agree.
		for _, name := range avatarRenditionLadder {
			for _, v := range media.Variants {
				if v.Name == name {
					return variantObject(media, v), true
				}
			}
		}
		return originalObject(media), true
	}
	for _, v := range media.Variants {
		if v.Name == variant {
			return variantObject(media, v), true
		}
	}
	return servedObject{}, false
}

// HeadForViewer answers HEAD /v1/media/:id/serve[/:variant] for viewerID
// (uuid.Nil is signed-out). Errors are the delivery package's, or an
// unclassified "no such rendition" — the handler answers both denial and the
// latter with the same not-found the GET gives.
func (h *ServeHeads) HeadForViewer(ctx context.Context, viewerID, mediaID uuid.UUID, variant string) (*ServeHead, error) {
	if h == nil || h.store == nil {
		return nil, fmt.Errorf("%w: media store not configured", delivery.ErrDeliveryUnresolved)
	}
	media, err := h.store.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, delivery.ErrDeliveryDenied
		}
		// A store fault is not "gone": clients and feed validators cache a
		// 404, so an outage answers 503.
		return nil, fmt.Errorf("%w: load media: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if media == nil {
		return nil, delivery.ErrDeliveryDenied
	}
	if DatingScopeDenies(media, viewerID) {
		return nil, delivery.ErrDeliveryDenied
	}

	// An anonymous-scoped asset is streamed by this service on GET, never
	// redirected. Its HEAD is the same authorization and then the rows: the
	// object is not opened, not even for its length.
	if media.AccessScope == postgres.AccessScopeAnonymous {
		if err := authorizeCaptionRead(ctx, h.gate, media, viewerID); err != nil {
			return nil, err
		}
		obj, ok := resolveServedObject(media, variant, false)
		if !ok {
			return nil, fmt.Errorf("variant %q not found", variant)
		}
		return serveHead(obj), nil
	}

	obj, ok := resolveServedObject(media, variant, true)
	if !ok {
		return nil, fmt.Errorf("variant %q not found", variant)
	}
	// The GET's own gate call, over the GET's own key. Only a read that named
	// a variant uses URLForVariant, exactly as in GetMediaVariantURL. A nil
	// gate is the gate's to refuse (unresolved), as it is for the GET.
	if variant == "original" || variant == AvatarVariant {
		_, err = h.gate.URLFor(ctx, viewerID.String(), mediaID.String(), obj.key)
	} else {
		_, err = h.gate.URLForVariant(ctx, viewerID.String(), mediaID.String(), obj.name, obj.key)
	}
	if err != nil {
		return nil, err
	}
	return serveHead(obj), nil
}

func serveHead(obj servedObject) *ServeHead {
	contentType := obj.mime
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &ServeHead{ContentType: contentType, Size: obj.size}
}
