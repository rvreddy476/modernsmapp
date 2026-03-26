package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDecodeJSONMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		want map[string]any
	}{
		{
			name: "empty",
			raw:  nil,
			want: map[string]any{},
		},
		{
			name: "invalid json",
			raw:  []byte("{not json"),
			want: map[string]any{},
		},
		{
			name: "valid object",
			raw:  []byte(`{"title":"Hello","count":3,"nested":{"enabled":true}}`),
			want: map[string]any{"title": "Hello", "count": float64(3), "nested": map[string]any{"enabled": true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := decodeJSONMap(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("decodeJSONMap() length = %d, want %d", len(got), len(tt.want))
			}
			wantJSON, _ := json.Marshal(tt.want)
			gotJSON, _ := json.Marshal(got)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("decodeJSONMap() = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestScanSlambook(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Round(time.Second)
	opensAt := now.Add(-2 * time.Hour)
	closesAt := now.Add(24 * time.Hour)
	deletedAt := now.Add(48 * time.Hour)
	contextID := uuid.New()
	coverID := uuid.New()
	subtitle := "sub"
	description := "desc"

	want := &Slambook{
		ID:                   uuid.New(),
		OwnerUserID:          uuid.New(),
		ContextType:          "profile",
		ContextID:            &contextID,
		Title:                "Springbook",
		Subtitle:             &subtitle,
		Description:          &description,
		Category:             "profile",
		ThemeKey:             "classic",
		CoverMediaID:         &coverID,
		Visibility:           "public",
		ResponseIdentityMode: "named",
		ApprovalRequired:     true,
		AllowCustomCards:     true,
		AllowReactions:       false,
		AllowComments:        true,
		AllowShareLink:       true,
		MaxResponsesPerUser:  1,
		OpensAt:              opensAt,
		ClosesAt:             &closesAt,
		Status:               "active",
		InvitedCount:         9,
		ResponseCount:        12,
		ApprovedCount:        8,
		PinnedCount:          2,
		LastActivityAt:       now.Add(2 * time.Hour),
		CreatedAt:            now.Add(-48 * time.Hour),
		UpdatedAt:            now.Add(-24 * time.Hour),
		DeletedAt:            &deletedAt,
	}

	scanner := fakeSlambookScanner{
		values: []any{
			want.ID, want.OwnerUserID, want.ContextType, want.ContextID,
			want.Title, want.Subtitle, want.Description, want.Category,
			want.ThemeKey, want.CoverMediaID, want.Visibility, want.ResponseIdentityMode,
			want.ApprovalRequired, want.AllowCustomCards, want.AllowReactions, want.AllowComments,
			want.AllowShareLink, want.MaxResponsesPerUser, want.OpensAt, want.ClosesAt,
			want.Status, want.InvitedCount, want.ResponseCount, want.ApprovedCount,
			want.PinnedCount, want.LastActivityAt, want.CreatedAt, want.UpdatedAt, want.DeletedAt,
		},
	}

	var got Slambook
	if err := scanSlambook(scanner, &got); err != nil {
		t.Fatalf("scanSlambook() error = %v", err)
	}

	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("scanSlambook() = %#v, want %#v", &got, want)
	}
}

func TestScanSlambookReturnsScannerError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	err := scanSlambook(fakeSlambookScanner{err: wantErr}, &Slambook{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("scanSlambook() error = %v, want %v", err, wantErr)
	}
}

type fakeSlambookScanner struct {
	values []any
	err    error
}

func (f fakeSlambookScanner) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if len(dest) != len(f.values) {
		return fmt.Errorf("unexpected destination count: got %d want %d", len(dest), len(f.values))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], f.values[i]); err != nil {
			return fmt.Errorf("assign %d: %w", i, err)
		}
	}
	return nil
}

func assignScanValue(dest any, value any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer")
	}
	return assignReflectValue(rv.Elem(), value)
}

func assignReflectValue(target reflect.Value, value any) error {
	if target.Kind() == reflect.Ptr {
		if value == nil {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		source := reflect.ValueOf(value)
		if source.Kind() == reflect.Ptr {
			if source.IsNil() {
				target.Set(reflect.Zero(target.Type()))
				return nil
			}
			if target.IsNil() {
				target.Set(reflect.New(target.Type().Elem()))
			}
			return assignReflectValue(target.Elem(), source.Elem().Interface())
		}
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		return assignReflectValue(target.Elem(), value)
	}

	if value == nil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}

	source := reflect.ValueOf(value)
	if !source.Type().AssignableTo(target.Type()) {
		if source.Type().ConvertibleTo(target.Type()) {
			source = source.Convert(target.Type())
		} else {
			return fmt.Errorf("cannot assign %s to %s", source.Type(), target.Type())
		}
	}
	target.Set(source)
	return nil
}
