package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CropParams holds normalized crop and focal point parameters.
type CropParams struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	FocalX float64 `json:"focal_x"`
	FocalY float64 `json:"focal_y"`
}

// Allowed slot names and owner types.
var (
	allowedSlotNames  = map[string]bool{"avatar": true, "banner": true, "watermark": true, "cover": true, "intro_video": true}
	allowedOwnerTypes = map[string]bool{"profile": true, "channel": true, "group": true, "module_profile": true}
)

// AssignSlot validates and assigns a media asset to a slot for a given owner entity.
func (s *Service) AssignSlot(ctx context.Context, userID uuid.UUID, ownerType, ownerID, slotName string, mediaAssetID uuid.UUID, crop *CropParams) error {
	// Validate owner type
	if !allowedOwnerTypes[ownerType] {
		return fmt.Errorf("invalid owner_type: %s", ownerType)
	}

	// Validate slot name
	if !allowedSlotNames[slotName] {
		return fmt.Errorf("invalid slot_name: %s", slotName)
	}

	// Parse owner ID
	ownerUUID, err := uuid.Parse(ownerID)
	if err != nil {
		return fmt.Errorf("invalid owner_id: %w", err)
	}

	// Verify the media asset exists and belongs to the user (or check ownership)
	media, err := s.pgStore.GetMedia(ctx, mediaAssetID)
	if err != nil {
		return fmt.Errorf("media asset not found")
	}
	if media.UploaderID != userID {
		return fmt.Errorf("forbidden: you do not own this media asset")
	}

	// Verify the media is at least uploaded
	if media.ProcessingStatus == "pending_upload" || media.ProcessingStatus == "rejected" || media.ProcessingStatus == "deleted" {
		return fmt.Errorf("media asset is not available (status: %s)", media.ProcessingStatus)
	}

	// Build the slot record
	slot := postgres.OwnerMediaSlot{
		OwnerType:    ownerType,
		OwnerID:      ownerUUID,
		SlotName:     slotName,
		MediaAssetID: mediaAssetID,
		FocalX:       0.5,
		FocalY:       0.5,
	}

	if crop != nil {
		slot.CropX = &crop.X
		slot.CropY = &crop.Y
		slot.CropW = &crop.W
		slot.CropH = &crop.H
		slot.FocalX = crop.FocalX
		slot.FocalY = crop.FocalY
	}

	if err := s.pgStore.AssignSlot(ctx, slot); err != nil {
		return fmt.Errorf("assign slot: %w", err)
	}

	slog.Info("slot assigned",
		"owner_type", ownerType, "owner_id", ownerID,
		"slot_name", slotName, "media_asset_id", mediaAssetID)
	return nil
}

// RemoveSlot removes the active slot for a given owner entity and slot name.
func (s *Service) RemoveSlot(ctx context.Context, userID uuid.UUID, ownerType, ownerID, slotName string) error {
	if !allowedOwnerTypes[ownerType] {
		return fmt.Errorf("invalid owner_type: %s", ownerType)
	}

	ownerUUID, err := uuid.Parse(ownerID)
	if err != nil {
		return fmt.Errorf("invalid owner_id: %w", err)
	}

	if err := s.pgStore.RemoveSlot(ctx, ownerType, ownerUUID, slotName); err != nil {
		return fmt.Errorf("remove slot: %w", err)
	}

	slog.Info("slot removed",
		"owner_type", ownerType, "owner_id", ownerID, "slot_name", slotName)
	return nil
}

// GetOwnerMedia returns all resolved media slots for an owner entity.
func (s *Service) GetOwnerMedia(ctx context.Context, ownerType string, ownerID uuid.UUID) ([]postgres.ResolvedSlot, error) {
	return s.pgStore.GetActiveSlots(ctx, ownerType, ownerID)
}

// GetOwnerMediaBatch returns resolved media slots for multiple owner entities.
func (s *Service) GetOwnerMediaBatch(ctx context.Context, owners []postgres.OwnerRef) (map[string][]postgres.ResolvedSlot, error) {
	if len(owners) > 100 {
		return nil, fmt.Errorf("batch limit is 100 owners")
	}
	return s.pgStore.GetResolvedBatch(ctx, owners)
}

// ActivatePendingSlots activates any pending slots referencing the given media asset.
// This should be called after a media asset finishes processing and becomes 'ready'.
func (s *Service) ActivatePendingSlots(ctx context.Context, mediaAssetID uuid.UUID) error {
	if err := s.pgStore.ActivatePendingSlot(ctx, mediaAssetID); err != nil {
		slog.Warn("failed to activate pending slots",
			"media_asset_id", mediaAssetID, "error", err)
		return err
	}
	return nil
}
