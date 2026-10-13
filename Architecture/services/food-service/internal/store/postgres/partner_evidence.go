package postgres

import (
	"context"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"strings"
)

func (s *Store) AdminEvidence(ctx context.Context, delivery bool, parent, id uuid.UUID) (*ownedmedia.Reference, error) {
	var ref ownedmedia.Reference
	query := `SELECT d.media_id,p.owner_user_id,d.document_type FROM food.restaurant_documents d JOIN food.restaurants r ON r.id=d.restaurant_id JOIN food.restaurant_partners p ON p.id=r.partner_id WHERE d.id=$1 AND d.restaurant_id=$2 AND d.media_id IS NOT NULL`
	if delivery {
		query = `SELECT d.media_id,p.user_id,d.document_type FROM food.delivery_partner_documents d JOIN food.delivery_partners p ON p.id=d.delivery_partner_id WHERE d.id=$1 AND d.delivery_partner_id=$2 AND d.media_id IS NOT NULL`
	}
	err := s.db.QueryRow(ctx, query, id, parent).Scan(&ref.MediaID, &ref.OwnerID, &ref.Kind)
	ref.Kind = strings.ToLower(ref.Kind)
	return &ref, err
}

// Review metadata deliberately excludes document numbers and storage URLs.
func (s *Store) AdminRestaurantEvidenceList(ctx context.Context, id uuid.UUID) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `SELECT id,document_type,status,media_id IS NOT NULL FROM food.restaurant_documents WHERE restaurant_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var doc uuid.UUID
		var kind, status string
		var viewable bool
		if err := rows.Scan(&doc, &kind, &status, &viewable); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": doc, "document_type": kind, "status": status, "viewable": viewable})
	}
	return out, rows.Err()
}
