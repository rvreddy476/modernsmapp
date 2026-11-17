package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// REG-3: a video whose length the confirm probe cannot read (WebM from a
// browser, MKV) used to be charged as long, so with two long videos in
// flight the third short WebM clip was refused at confirm, after its upload.

func TestUnknownLengthClipIsAdmittedWithTwoLongVideosInFlight(t *testing.T) {
	l := loadUploadLimits(func(string) string { return "" }) // defaults: cap 2, bound 512 MiB
	st := &fakeQuotaStore{usage: postgres.UploadUsage{InflightLong: 2}}
	clip := &postgres.MediaAsset{ID: uuid.New(), UploaderID: uuid.New(), FileType: "video"}
	probeErr := func() (float64, error) { return 0, errors.New("not an MP4/MOV header") }
	reject := func() error { t.Fatal("an unknown length was rejected"); return nil }

	if err := admitVideoAtConfirm(context.Background(), st, l, time.Now(), clip, 25<<20, probeErr, reject); err != nil {
		t.Fatalf("third WebM clip refused: %v", err)
	}
	if c := st.charged[clip.ID]; c.IsLong || c.Bytes != 25<<20 || c.DurationMs != 0 {
		t.Fatalf("charge %+v: bytes yes, seconds no, not long", c)
	}

	// The bytes budget still applies to an unknown length.
	l.DailyVideoBytes = 30 << 20
	over := &postgres.MediaAsset{ID: uuid.New(), UploaderID: clip.UploaderID, FileType: "video"}
	if err := admitVideoAtConfirm(context.Background(), st, l, time.Now(), over, 10<<20, probeErr, reject); !errors.Is(err, ErrUploadQuotaExceeded) {
		t.Fatalf("bytes budget on an unknown length: %v", err)
	}

	// Over the bound it is still a long video and meets the cap.
	l.DailyVideoBytes = 0
	big := &postgres.MediaAsset{ID: uuid.New(), UploaderID: clip.UploaderID, FileType: "video"}
	if err := admitVideoAtConfirm(context.Background(), st, l, time.Now(), big, l.UnknownLengthShortMaxBytes+1, probeErr, reject); !errors.Is(err, ErrLongVideoInFlight) {
		t.Fatalf("large unknown length with two in flight: %v", err)
	}

	// 0 restores the old rule: every unknown length is long.
	l.UnknownLengthShortMaxBytes = 0
	strict := &postgres.MediaAsset{ID: uuid.New(), UploaderID: clip.UploaderID, FileType: "video"}
	if err := admitVideoAtConfirm(context.Background(), st, l, time.Now(), strict, 1<<20, probeErr, reject); !errors.Is(err, ErrLongVideoInFlight) {
		t.Fatalf("bound 0: %v", err)
	}
}

// The refusal that confirm is bound to make comes at init, before the
// upload: a large WebM with no declared length while the cap is full.
func TestLargeUndeclaredWebMIsRefusedAtInitWhenTheLongCapIsFull(t *testing.T) {
	l := loadUploadLimits(func(string) string { return "" })
	st := &fakeQuotaStore{usage: postgres.UploadUsage{InflightLong: 2}}
	ctx, now, user := context.Background(), time.Now(), uuid.New()

	if err := videoBudgetAtInit(ctx, st, l, now, user, l.UnknownLengthShortMaxBytes+1, 0, "video/webm"); !errors.Is(err, ErrLongVideoInFlight) {
		t.Fatalf("large undeclared WebM at init: %v", err)
	}
	// A short WebM is not refused; neither is an MP4 (its header decides at
	// confirm), nor a WebM small enough for confirm to probe whole.
	for _, c := range []struct {
		mime  string
		bytes int64
	}{
		{"video/webm", 20 << 20},
		{"video/webm", l.UnknownLengthShortMaxBytes},
		{"video/mp4", 4 << 30},
		{"video/quicktime; codecs=x", 4 << 30},
	} {
		if err := videoBudgetAtInit(ctx, st, l, now, user, c.bytes, 0, c.mime); err != nil {
			t.Fatalf("%s %d bytes at init: %v", c.mime, c.bytes, err)
		}
	}
	if len(st.charged) != 0 {
		t.Fatal("init charged the ledger")
	}
}

func TestWholeFileProbeReadsTheRealLength(t *testing.T) {
	ctx := context.Background()
	fetched := 0
	fetch := func(path string) error { fetched++; return os.WriteFile(path, []byte("webm"), 0o600) }
	probe := func(_ context.Context, path string) (*processing.VideoMeta, error) {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("probe ran on a missing file: %v", err)
		}
		return &processing.VideoMeta{DurationFloat: 31.5}, nil
	}
	if s, err := probeWholeFileSeconds(ctx, 10<<20, fetch, probe); err != nil || s != 31.5 {
		t.Fatalf("probe %v %v, want 31.5", s, err)
	}
	// Over the bound: nothing is fetched into the pod.
	if _, err := probeWholeFileSeconds(ctx, maxWholeFileProbeBytes+1, fetch, probe); err == nil || fetched != 1 {
		t.Fatalf("over the bound: err %v, fetched %d", err, fetched)
	}
	// A probe with no length is an unknown length, not zero seconds.
	none := func(context.Context, string) (*processing.VideoMeta, error) { return &processing.VideoMeta{}, nil }
	if _, err := probeWholeFileSeconds(ctx, 1<<20, fetch, none); err == nil {
		t.Fatal("no length reported must be an error")
	}
}

func TestUnknownLengthBoundEnv(t *testing.T) {
	if d := loadUploadLimits(func(string) string { return "" }); d.UnknownLengthShortMaxBytes != 512<<20 {
		t.Fatalf("default %d, want 512 MiB", d.UnknownLengthShortMaxBytes)
	}
	l := loadUploadLimits(func(k string) string {
		if k == UploadUnknownLengthShortMaxBytesEnv {
			return "0"
		}
		return ""
	})
	if l.UnknownLengthShortMaxBytes != 0 {
		t.Fatalf("override %d, want 0", l.UnknownLengthShortMaxBytes)
	}
}
