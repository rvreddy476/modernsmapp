package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) CreateAnswerRequest(ctx context.Context, questionID, requesterID, requestedUserID uuid.UUID) (*AnswerRequest, error) {
	r := &AnswerRequest{}
	err := s.db.QueryRow(ctx, `
		INSERT INTO answer_requests (question_id, requester_id, requested_user_id)
		VALUES ($1, $2, $3)
		RETURNING id, question_id, requester_id, requested_user_id, status, created_at`,
		questionID, requesterID, requestedUserID,
	).Scan(&r.ID, &r.QuestionID, &r.RequesterID, &r.RequestedUserID, &r.Status, &r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create answer request: %w", err)
	}
	return r, nil
}

func (s *Store) RespondToAnswerRequest(ctx context.Context, requestID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE answer_requests SET status = $2 WHERE id = $1`, requestID, status)
	return err
}

func (s *Store) GetAnswerRequestsForUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]AnswerRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, question_id, requester_id, requested_user_id, status, created_at
		FROM answer_requests WHERE requested_user_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AnswerRequest
	for rows.Next() {
		var r AnswerRequest
		if err := rows.Scan(&r.ID, &r.QuestionID, &r.RequesterID, &r.RequestedUserID, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *Store) GetAnswerRequestsByQuestion(ctx context.Context, questionID uuid.UUID) ([]AnswerRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, question_id, requester_id, requested_user_id, status, created_at
		FROM answer_requests WHERE question_id = $1 ORDER BY created_at DESC`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AnswerRequest
	for rows.Next() {
		var r AnswerRequest
		if err := rows.Scan(&r.ID, &r.QuestionID, &r.RequesterID, &r.RequestedUserID, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}
