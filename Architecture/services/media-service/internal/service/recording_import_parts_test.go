package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Multi-part live recordings (16 Nov 2026): keys are copied to parts/NNN.mp4
// in order and registered with no original for the worker to join.

func (f *recFixture) partKey(n int) string {
	if n == 1 {
		return f.key
	}
	return fmt.Sprintf("recordings/%s_part%d.mp4", f.stream, n)
}

func (f *recFixture) addPart(n int) string {
	k := f.partKey(n)
	f.blob.sources[testRecBucket+"/"+k] = fakeRecObject{data: mp4Bytes(1000 * n), etag: fmt.Sprintf("etag-%d", n)}
	return k
}

func TestRecordingImportParts_CopiesInOrderWithNoOriginal(t *testing.T) {
	f := newRecFixture(t) // part 1 = f.key, 4096 bytes
	k2, k3 := f.addPart(2), f.addPart(3)
	in := f.input()
	in.Key = k3 // the longest part, as live-service sends it
	in.Keys = []string{f.key, k2, k3}
	res, err := f.imp.Import(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "user/" + f.owner.String() + "/" + res.MediaID.String() + "/"
	want := []string{
		testRecBucket + "/" + f.key + " -> " + prefix + "parts/001.mp4",
		testRecBucket + "/" + k2 + " -> " + prefix + "parts/002.mp4",
		testRecBucket + "/" + k3 + " -> " + prefix + "parts/003.mp4",
	}
	if fmt.Sprint(f.blob.copies) != fmt.Sprint(want) {
		t.Fatalf("copies = %v\nwant %v", f.blob.copies, want)
	}
	row := f.store.created[0]
	if row.StorageKey != prefix+"original" || row.SizeBytes != 4096+2000+3000 || row.OriginalETag != "" {
		t.Fatalf("row = %+v", row)
	}
	if _, ok := f.blob.own[prefix+"original"]; ok {
		t.Fatal("an original was written; the worker joins it")
	}
}

func TestRecordingImportParts_MissingPartSkipped(t *testing.T) {
	f := newRecFixture(t)
	k3 := f.addPart(3)
	in := f.input()
	in.Keys = []string{f.key, f.partKey(2), k3} // part 2 never reached storage
	res, err := f.imp.Import(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "user/" + f.owner.String() + "/" + res.MediaID.String() + "/"
	if len(f.blob.copies) != 2 || f.blob.copies[1] != testRecBucket+"/"+k3+" -> "+prefix+"parts/002.mp4" {
		t.Fatalf("copies = %v", f.blob.copies)
	}
}

func TestRecordingImportParts_OneFoundIsTheSingleKeyPath(t *testing.T) {
	f := newRecFixture(t)
	in := f.input()
	in.Keys = []string{f.key, f.partKey(2)}
	res, err := f.imp.Import(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "user/" + f.owner.String() + "/" + res.MediaID.String() + "/original"
	if len(f.blob.copies) != 1 || f.blob.copies[0] != testRecBucket+"/"+f.key+" -> "+wantKey {
		t.Fatalf("copies = %v", f.blob.copies)
	}
	if f.store.created[0].OriginalETag != "copy-etag-1" {
		t.Fatalf("row = %+v", f.store.created[0])
	}
}

func TestRecordingImportParts_NoneFound(t *testing.T) {
	f := newRecFixture(t)
	delete(f.blob.sources, testRecBucket+"/"+f.key)
	in := f.input()
	in.Keys = []string{f.key, f.partKey(2)}
	if _, err := f.imp.Import(context.Background(), in); !errors.Is(err, ErrRecordingNotFound) {
		t.Fatalf("err = %v, want not found (retried)", err)
	}
	if len(f.store.created) != 0 {
		t.Fatal("registered with no part")
	}
}

func TestRecordingImportParts_TotalOverCapDropsNothingCopied(t *testing.T) {
	f := newRecFixture(t)
	f.imp.cfg.MaxBytes = 5000 // each part fits, the sum does not
	k2 := f.addPart(2)
	in := f.input()
	in.Keys = []string{f.key, k2}
	if _, err := f.imp.Import(context.Background(), in); !errors.Is(err, ErrRecordingTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if len(f.blob.copies) != 0 {
		t.Fatalf("copied before the size check: %v", f.blob.copies)
	}
}

func TestRecordingImportParts_FailedCopyDropsEarlierCopies(t *testing.T) {
	f := newRecFixture(t)
	in := f.input()
	in.Keys = []string{f.key, f.addPart(2)}
	f.store.insertErr = errors.New("db down")
	if _, err := f.imp.Import(context.Background(), in); err == nil {
		t.Fatal("no error")
	}
	if len(f.blob.own) != 0 || len(f.blob.deleted) != 2 {
		t.Fatalf("left copies: own=%v deleted=%v", f.blob.own, f.blob.deleted)
	}
}

func TestRecordingImportParts_Validation(t *testing.T) {
	f := newRecFixture(t)
	other := "recordings/00000000-0000-0000-0000-000000000001.mp4"
	cases := map[string]func(*RecordingImportInput){
		"duplicate":       func(in *RecordingImportInput) { in.Keys = []string{f.key, f.key} },
		"key not in keys": func(in *RecordingImportInput) { in.Key = f.partKey(9); in.Keys = []string{f.key, f.partKey(2)} },
		"another stream":  func(in *RecordingImportInput) { in.Keys = []string{f.key, other} },
		"traversal": func(in *RecordingImportInput) {
			in.Keys = []string{f.key, "recordings/../" + f.stream.String() + ".mp4"}
		},
		"too many": func(in *RecordingImportInput) { in.Keys = make([]string, MaxRecordingParts+1) },
	}
	for name, mut := range cases {
		in := f.input()
		mut(&in)
		if _, err := f.imp.Import(context.Background(), in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(f.blob.copies) != 0 {
		t.Fatalf("copied on a refused request: %v", f.blob.copies)
	}
}
