// Safety service tests.
//
// CRITICAL RULES #6: panic must persist before responding 200. Test the
// persist-before-emit ordering and the explicit error paths. The lane D8
// behaviour (incidents, trusted contacts, live location, meets, reports,
// purge retention) is covered in d8_safety_it_test.go.
package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSafety_PanicPersistsBeforeReturn(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	uid := uuid.New()
	lat, lng := 12.97, 77.59
	out, err := svc.RecordPanic(context.Background(), uid, PanicRequest{
		Latitude:  &lat,
		Longitude: &lng,
		Context:   map[string]any{"source": "test"},
	})
	if err != nil {
		t.Fatalf("panic: %v", err)
	}
	if _, err := st.GetPanicIncident(context.Background(), out.IncidentID); err != nil {
		t.Fatalf("incident not persisted: %v", err)
	}
}

func TestSafety_RejectsZeroUserID(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	if _, err := svc.RecordPanic(context.Background(), uuid.Nil, PanicRequest{}); err == nil {
		t.Fatalf("expected error on zero user id")
	}
}
