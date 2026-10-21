package service

import (
	"errors"
	"testing"
)

const gib = int64(1024) * mib

// chunk = max(5 MiB, ceil(total/9000)) rounded up to a whole MiB, at most
// 64 MiB; refuse past 10,000 parts (founder, 2026-10-21).
func TestResumableChunkPlan(t *testing.T) {
	cases := []struct {
		name      string
		total     int64
		wantChunk int64
		wantParts int
	}{
		// The 5 MiB floor (S3's minimum part size).
		{"one byte", 1, 5 * mib, 1},
		{"exactly 5 MiB", 5 * mib, 5 * mib, 1},
		{"5 MiB + 1", 5*mib + 1, 5 * mib, 2},
		{"500 MB", 500 * 1000 * 1000, 5 * mib, 96},
		{"9000 parts at the floor", 9000 * 5 * mib, 5 * mib, 9000},
		// Scaling: past 9000 x 5 MiB the chunk grows, in whole MiB.
		{"one byte past the floor's reach rounds up to 6 MiB", 9000*5*mib + 1, 6 * mib, 7501},
		{"50 GiB", 50 * gib, 6 * mib, 8534},
		{"100 GiB", 100 * gib, 12 * mib, 8534},
		{"500 GiB", 500 * gib, 57 * mib, 8983},
		// The 64 MiB cap (Cloudflare's ~100 MB request limit).
		{"576 GiB reaches the cap", 9000 * 64 * mib, 64 * mib, 9000},
		{"600 GiB at the cap", 600 * gib, 64 * mib, 9600},
		{"625 GiB is exactly 10,000 parts", 10_000 * 64 * mib, 64 * mib, 10_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunk, parts, err := resumableChunkPlan(tc.total)
			if err != nil {
				t.Fatalf("resumableChunkPlan(%d): %v", tc.total, err)
			}
			if int64(chunk) != tc.wantChunk || parts != tc.wantParts {
				t.Fatalf("resumableChunkPlan(%d) = (%d MiB, %d parts), want (%d MiB, %d parts)",
					tc.total, int64(chunk)/mib, parts, tc.wantChunk/mib, tc.wantParts)
			}
		})
	}
}

func TestResumableChunkPlanRefusesPastTenThousandParts(t *testing.T) {
	if MaxResumableUploadBytes != 625*gib {
		t.Fatalf("MaxResumableUploadBytes = %d, want 625 GiB", MaxResumableUploadBytes)
	}
	for _, total := range []int64{MaxResumableUploadBytes + 1, 700 * gib, 5 * 1024 * gib} {
		_, _, err := resumableChunkPlan(total)
		if !errors.Is(err, ErrResumableUploadTooLarge) {
			t.Fatalf("resumableChunkPlan(%d) err = %v, want ErrResumableUploadTooLarge", total, err)
		}
	}
	for _, total := range []int64{0, -1} {
		if _, _, err := resumableChunkPlan(total); err == nil {
			t.Fatalf("resumableChunkPlan(%d) accepted a non-positive size", total)
		}
	}
}

// The invariants every plan holds, over a sweep of sizes: whole MiB within
// [5, 64] MiB, the parts exactly cover the file, at most 10,000 parts, and
// at most 9,000 unless the chunk is already at the cap.
func TestResumableChunkPlanInvariants(t *testing.T) {
	sizes := []int64{1, mib, 5*mib - 1, 5 * mib}
	for s := int64(10 * mib); s <= MaxResumableUploadBytes; s = s*13/10 + 7 {
		sizes = append(sizes, s)
	}
	sizes = append(sizes, MaxResumableUploadBytes-1, MaxResumableUploadBytes)
	for _, total := range sizes {
		chunk, parts, err := resumableChunkPlan(total)
		if err != nil {
			t.Fatalf("resumableChunkPlan(%d): %v", total, err)
		}
		c := int64(chunk)
		if c%mib != 0 || c < 5*mib || c > 64*mib {
			t.Fatalf("total %d: chunk %d is not a whole MiB in [5, 64] MiB", total, c)
		}
		if int64(parts)*c < total || int64(parts-1)*c >= total {
			t.Fatalf("total %d: %d parts of %d do not exactly cover the file", total, parts, c)
		}
		if parts > 10_000 {
			t.Fatalf("total %d: %d parts", total, parts)
		}
		if parts > 9_000 && c != 64*mib {
			t.Fatalf("total %d: %d parts with a %d MiB chunk (should have grown)", total, parts, c/mib)
		}
	}
}
