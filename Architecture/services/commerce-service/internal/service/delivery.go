package service

import (
	"context"
	"errors"
	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) DeliveryProfile(ctx context.Context, user uuid.UUID) (*postgres.DeliveryProfile, error) {
	return s.store.DeliveryProfile(ctx, user)
}
func (s *Service) SaveDeliveryProfile(ctx context.Context, user uuid.UUID, name, vehicle string) error {
	return s.store.SaveDeliveryProfile(ctx, user, name, vehicle)
}
func (s *Service) SubmitDeliveryProfile(ctx context.Context, user uuid.UUID) error {
	return s.store.SubmitDeliveryProfile(ctx, user)
}
func (s *Service) SaveDeliveryDocument(ctx context.Context, user uuid.UUID, kind string, id uuid.UUID) error {
	if !postgres.ValidDeliveryDocument(kind) || id == uuid.Nil {
		return postgres.ErrDeliveryInvalid
	}
	// No development bypass for identity evidence.
	if s.media == nil {
		return media.ErrMediaUnavailable
	}
	expected := media.KindImage
	if kind == "introduction" {
		expected = media.KindVideo
	}
	if err := s.media.VerifyOwned(ctx, id, user, expected); err != nil {
		return err
	}
	if kind == "introduction" {
		asset, err := s.media.Get(ctx, id)
		if err != nil {
			return err
		}
		if asset.DurationSeconds == nil || *asset.DurationSeconds <= 0 || *asset.DurationSeconds > 60 {
			return postgres.ErrDeliveryInvalid
		}
	}
	return s.store.SaveDeliveryDocument(ctx, user, kind, id)
}
func (s *Service) ReviewDelivery(ctx context.Context, actor, profile, doc uuid.UUID, revision int64, decision, note string) error {
	return s.store.ReviewDelivery(ctx, actor, profile, doc, revision, decision, note)
}
func (s *Service) DeliveryReviewQueue(ctx context.Context) ([]postgres.DeliveryProfile, error) {
	return s.store.DeliveryReviewQueue(ctx)
}
func (s *Service) AdminDeliveryProfile(ctx context.Context, profile uuid.UUID) (*postgres.DeliveryProfile, error) {
	var user uuid.UUID
	if err := s.store.DB().QueryRow(ctx, `SELECT user_id FROM commerce_delivery_profiles WHERE id=$1`, profile).Scan(&user); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, postgres.ErrDeliveryProfileMissing
		}
		return nil, err
	}
	return s.store.DeliveryProfile(ctx, user)
}

func (s *Service) AdminDeliveryDocumentImage(ctx context.Context, profile, document, actor uuid.UUID) (*SellerDocumentImage, error) {
	if actor == uuid.Nil {
		return nil, postgres.ErrActorRequired
	}
	var id uuid.UUID
	var kind string
	err := s.store.DB().QueryRow(ctx, `SELECT media_id,kind FROM commerce_delivery_documents WHERE id=$1 AND profile_id=$2`, document, profile).Scan(&id, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, postgres.ErrSellerDocumentNotFound
	}
	if err != nil {
		return nil, err
	}
	if kind == "introduction" {
		return nil, ErrDocumentImageUnavailable
	}
	if s.media == nil {
		return nil, media.ErrMediaUnavailable
	}
	body, typ, err := s.media.FetchImageBytes(ctx, id)
	if err != nil {
		return nil, err
	}
	// Audit succeeds before any document bytes leave the service.
	_, err = s.store.DB().Exec(ctx, `INSERT INTO commerce_delivery_review_audit(profile_id,document_id,actor_id,decision) VALUES($1,$2,$3,'document_view')`, profile, document, actor)
	if err != nil {
		body.Close()
		return nil, err
	}
	return &SellerDocumentImage{Body: body, ContentType: typ, DocumentType: kind}, nil
}

// Resolve an owned reference only after module-level permission/ownership
// checks. Admin-service then uses its signed, audited media evidence stream;
// no presigned URL or arbitrary caller-supplied media id is exposed.
func (s *Service) AdminDeliveryEvidence(ctx context.Context, profile, document, actor uuid.UUID) (ownedmedia.Reference, error) {
	var ref ownedmedia.Reference
	if actor == uuid.Nil {
		return ref, postgres.ErrActorRequired
	}
	err := s.store.DB().QueryRow(ctx, `SELECT d.media_id,p.user_id,d.kind FROM commerce_delivery_documents d JOIN commerce_delivery_profiles p ON p.id=d.profile_id WHERE d.id=$1 AND p.id=$2`, document, profile).Scan(&ref.MediaID, &ref.OwnerID, &ref.Kind)
	if err != nil {
		return ref, err
	}
	if ref.Kind == "introduction" {
		ref.Kind = "introduction_video"
	}
	_, err = s.store.DB().Exec(ctx, `INSERT INTO commerce_delivery_review_audit(profile_id,document_id,actor_id,decision) VALUES($1,$2,$3,'document_view')`, profile, document, actor)
	return ref, err
}
