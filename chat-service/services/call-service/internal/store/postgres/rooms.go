package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *CallStore) CreateRoom(ctx context.Context, room *domain.CallRoom) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO calls.call_rooms
			(id, room_key, provider, provider_room_name, region_code,
			 assigned_node_id, status, max_participants, created_at, expires_at, metadata_json)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		room.ID, room.RoomKey, room.Provider, room.ProviderRoomName,
		room.RegionCode, room.AssignedNodeID, room.Status,
		room.MaxParticipants, room.CreatedAt, room.ExpiresAt, room.MetadataJSON,
	)
	return err
}

func (s *CallStore) GetRoomByKey(ctx context.Context, roomKey string) (*domain.CallRoom, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, room_key, provider, provider_room_name, region_code,
		       assigned_node_id, status, max_participants, created_at, expires_at, metadata_json
		FROM calls.call_rooms WHERE room_key = $1`, roomKey)

	var r domain.CallRoom
	err := row.Scan(
		&r.ID, &r.RoomKey, &r.Provider, &r.ProviderRoomName, &r.RegionCode,
		&r.AssignedNodeID, &r.Status, &r.MaxParticipants,
		&r.CreatedAt, &r.ExpiresAt, &r.MetadataJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *CallStore) UpdateRoomStatus(ctx context.Context, roomID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_rooms SET status = $2 WHERE id = $1`,
		roomID, status,
	)
	return err
}

func (s *CallStore) GetRoom(ctx context.Context, roomID uuid.UUID) (*domain.CallRoom, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, room_key, provider, provider_room_name, region_code,
		       assigned_node_id, status, max_participants, created_at, expires_at, metadata_json
		FROM calls.call_rooms WHERE id = $1`, roomID)

	var r domain.CallRoom
	err := row.Scan(
		&r.ID, &r.RoomKey, &r.Provider, &r.ProviderRoomName, &r.RegionCode,
		&r.AssignedNodeID, &r.Status, &r.MaxParticipants,
		&r.CreatedAt, &r.ExpiresAt, &r.MetadataJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// CloseExpiredRooms closes rooms that have passed their expires_at.
func (s *CallStore) CloseExpiredRooms(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE calls.call_rooms SET status = 'closed'
		WHERE status IN ('allocated', 'active') AND expires_at IS NOT NULL AND expires_at < $1`,
		time.Now(),
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
