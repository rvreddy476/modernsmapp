package main

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/google/uuid"
)

type fakeScanWriter struct {
	calls   int
	id      uuid.UUID
	labels  string
	scanner string
	fail    error
}

func (f *fakeScanWriter) UpdateMediaModerationScan(_ context.Context, id uuid.UUID, labels []byte, scanner string) error {
	f.calls++
	f.id, f.labels, f.scanner = id, string(labels), scanner
	return f.fail
}

// Chat parity pass 2: a passing video's labels and scanner are stored, so
// media-service can say whether it is sensitive. Removing the call in
// recordVideoScanLabels fails this.
func TestRecordVideoScanLabelsStoresPassingScan(t *testing.T) {
	w := &fakeScanWriter{}
	id := uuid.New()
	res := processing.ScanResult{IsSafe: true, Labels: []processing.ModerationLabel{{Name: "Suggestive", Confidence: 91}}}
	if err := recordVideoScanLabels(context.Background(), w, id, processing.NewMockModerationScanner(), res); err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || w.id != id || w.scanner != "mock" || w.labels != `[{"name":"Suggestive","confidence":91}]` {
		t.Errorf("stored %d call(s): id=%s scanner=%q labels=%s", w.calls, w.id, w.scanner, w.labels)
	}

	// A clean scan is stored as [] — scanned, nothing found — not NULL.
	w = &fakeScanWriter{}
	if err := recordVideoScanLabels(context.Background(), w, id, processing.NewMockModerationScanner(), processing.ScanResult{IsSafe: true}); err != nil {
		t.Fatal(err)
	}
	if w.labels != "[]" {
		t.Errorf("clean scan stored %q, want []", w.labels)
	}
}

func TestRecordVideoScanLabelsSkipsRejectedAndSurfacesStoreErrors(t *testing.T) {
	w := &fakeScanWriter{}
	if err := recordVideoScanLabels(context.Background(), w, uuid.New(), &processing.StubScanner{}, processing.ScanResult{IsSafe: false, Reason: "violence"}); err != nil || w.calls != 0 {
		t.Errorf("rejected video: err=%v calls=%d, want no write", err, w.calls)
	}
	w = &fakeScanWriter{fail: errors.New("database unavailable")}
	if err := recordVideoScanLabels(context.Background(), w, uuid.New(), &processing.StubScanner{}, processing.ScanResult{IsSafe: true}); err == nil {
		t.Error("a failed label write was swallowed; the transcode must retry")
	}
}
