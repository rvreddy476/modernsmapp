package service

import (
	"strings"
	"testing"
	"time"

	"github.com/atpost/memories-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestCreateSlambookValidation(t *testing.T) {
	t.Parallel()

	ownerID := uuid.New()
	baseInput := &CreateSlambookInput{
		OwnerUserID:     ownerID,
		Title:           "Summer Slam",
		TemplatePackKey: "friendship",
		CustomCards: []CreateSlambookCardInput{
			{Prompt: "Prompt", ResponseType: "text"},
		},
	}

	tests := []struct {
		name    string
		mutate  func(*CreateSlambookInput)
		wantErr string
	}{
		{
			name: "missing title",
			mutate: func(input *CreateSlambookInput) {
				input.Title = "   "
			},
			wantErr: "title is required",
		},
		{
			name: "missing owner",
			mutate: func(input *CreateSlambookInput) {
				input.OwnerUserID = uuid.Nil
			},
			wantErr: "owner user id is required",
		},
		{
			name: "unsupported visibility",
			mutate: func(input *CreateSlambookInput) {
				input.Visibility = "friends_only"
			},
			wantErr: "unsupported visibility for v1",
		},
		{
			name: "invalid identity mode",
			mutate: func(input *CreateSlambookInput) {
				input.ResponseIdentityMode = "voice_only"
			},
			wantErr: "invalid response identity mode",
		},
		{
			name: "requires at least one card",
			mutate: func(input *CreateSlambookInput) {
				input.TemplatePackKey = "   "
				input.CustomCards = nil
			},
			wantErr: "choose a template pack or add at least one custom card",
		},
		{
			name: "custom card response type restricted",
			mutate: func(input *CreateSlambookInput) {
				input.TemplatePackKey = "   "
				input.CustomCards = []CreateSlambookCardInput{
					{Prompt: "Tell us", ResponseType: "rating"},
				}
			},
			wantErr: "custom card response type rating is not supported in v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := *baseInput
			input.CustomCards = append([]CreateSlambookCardInput(nil), baseInput.CustomCards...)
			tt.mutate(&input)

			svc := &Service{}
			_, err := svc.CreateSlambook(t.Context(), &input)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCanViewSlambook(t *testing.T) {
	t.Parallel()

	ownerID := uuid.New()
	otherID := uuid.New()
	slambook := &postgres.Slambook{
		OwnerUserID: ownerID,
		Visibility:  "private",
	}

	if !canViewSlambook(slambook, &ownerID, false) {
		t.Fatalf("expected owner to view private slambook")
	}
	if canViewSlambook(slambook, &otherID, false) {
		t.Fatalf("expected non-owner to be blocked from private slambook")
	}
	if canViewSlambook(slambook, nil, false) {
		t.Fatalf("expected anonymous viewer to be blocked from private slambook")
	}

	slambook.Visibility = "public"
	if !canViewSlambook(slambook, &otherID, false) {
		t.Fatalf("expected public slambook to be visible")
	}
	if !canViewSlambook(slambook, nil, false) {
		t.Fatalf("expected anonymous viewer to see public slambook")
	}

	slambook.Visibility = "invited_only"
	if canViewSlambook(slambook, &otherID, false) {
		t.Fatalf("expected invite-only slambook to be hidden without invite")
	}
	if !canViewSlambook(slambook, &otherID, true) {
		t.Fatalf("expected invite-only slambook to be visible with invite")
	}
}

func TestCanRespondToSlambook(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	open := now.Add(-time.Hour)
	close := now.Add(time.Hour)
	ownerID := uuid.New()
	responderID := uuid.New()

	tests := []struct {
		name      string
		slambook  *postgres.Slambook
		responder uuid.UUID
		hasInvite bool
		want      bool
	}{
		{
			name:      "nil slambook",
			slambook:  nil,
			responder: responderID,
			hasInvite: true,
			want:      false,
		},
		{
			name: "owner cannot respond",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "public",
				Status:      "active",
				OpensAt:     open,
				ClosesAt:    &close,
			},
			responder: ownerID,
			hasInvite: true,
			want:      false,
		},
		{
			name: "inactive slambook",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "public",
				Status:      "archived",
				OpensAt:     open,
				ClosesAt:    &close,
			},
			responder: responderID,
			hasInvite: true,
			want:      false,
		},
		{
			name: "not opened yet",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "public",
				Status:      "active",
				OpensAt:     now.Add(time.Hour),
				ClosesAt:    &close,
			},
			responder: responderID,
			hasInvite: true,
			want:      false,
		},
		{
			name: "already closed",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "public",
				Status:      "active",
				OpensAt:     open,
				ClosesAt:    &open,
			},
			responder: responderID,
			hasInvite: true,
			want:      false,
		},
		{
			name: "public visible",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "public",
				Status:      "active",
				OpensAt:     open,
				ClosesAt:    &close,
			},
			responder: responderID,
			hasInvite: false,
			want:      true,
		},
		{
			name: "invite required without invite",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "invited_only",
				Status:      "active",
				OpensAt:     open,
				ClosesAt:    &close,
			},
			responder: responderID,
			hasInvite: false,
			want:      false,
		},
		{
			name: "invite allows response",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "invited_only",
				Status:      "active",
				OpensAt:     open,
				ClosesAt:    &close,
			},
			responder: responderID,
			hasInvite: true,
			want:      true,
		},
		{
			name: "friends visibility is invite gated in v1",
			slambook: &postgres.Slambook{
				OwnerUserID: ownerID,
				Visibility:  "friends_only",
				Status:      "active",
				OpensAt:     open,
				ClosesAt:    &close,
			},
			responder: responderID,
			hasInvite: true,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := canRespondToSlambook(tt.slambook, tt.responder, tt.hasInvite)
			if got != tt.want {
				t.Fatalf("canRespondToSlambook() = %v, want %v", got, tt.want)
			}
		})
	}
}
