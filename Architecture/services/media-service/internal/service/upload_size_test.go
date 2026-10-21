package service

import (
	"errors"
	"testing"
)

// Simple (single presigned PUT) init: a video may be up to 5 GiB, S3's
// single-PUT maximum; past that the answer is "use the resumable upload",
// not a refusal. Everything else keeps the 500 MB absolute cap.
func TestValidateUploadSizeSimpleVideoCap(t *testing.T) {
	if MaxSimpleVideoUploadBytes != 5*1024*1024*1024 {
		t.Fatalf("MaxSimpleVideoUploadBytes = %d, want 5 GiB", MaxSimpleVideoUploadBytes)
	}
	for _, size := range []int64{1, MaxUploadSizeBytes + 1, 2 * 1024 * 1024 * 1024, MaxSimpleVideoUploadBytes} {
		if err := ValidateUploadSize("video", size); err != nil {
			t.Fatalf("video of %d bytes refused on the simple path: %v", size, err)
		}
	}
	err := ValidateUploadSize("video", MaxSimpleVideoUploadBytes+1)
	if !errors.Is(err, ErrUseResumableUpload) {
		t.Fatalf("video one byte over 5 GiB: err = %v, want ErrUseResumableUpload", err)
	}
	// Non-video keeps the old absolute cap, and is a plain refusal.
	for _, ft := range []string{"image", "audio"} {
		if err := ValidateUploadSize(ft, MaxUploadSizeBytes); err != nil {
			t.Fatalf("%s at the cap refused: %v", ft, err)
		}
		err := ValidateUploadSize(ft, MaxUploadSizeBytes+1)
		if err == nil || errors.Is(err, ErrUseResumableUpload) {
			t.Fatalf("%s over 500 MB: err = %v, want a plain file-too-large", ft, err)
		}
	}
}

// ValidateUpload no longer caps video size (resumable path: no total cap
// for video); image caps are unchanged.
func TestValidateUploadVideoHasNoSizeCap(t *testing.T) {
	for _, size := range []int64{MaxVideoSize + 1, 50 * 1024 * 1024 * 1024, MaxResumableUploadBytes} {
		if err := ValidateUpload("video", "general", "video/mp4", size); err != nil {
			t.Fatalf("video of %d bytes refused: %v", size, err)
		}
	}
	if err := ValidateUpload("image", "general", "image/jpeg", MaxImageSize+1); err == nil {
		t.Fatal("image over 20 MB accepted")
	}
	if err := ValidateUpload("image", "general", "image/jpeg", MaxImageSize); err != nil {
		t.Fatalf("image at 20 MB refused: %v", err)
	}
	if err := ValidateUpload("video", "general", "video/x-flv", 1024); err == nil {
		t.Fatal("unsupported video mime accepted")
	}
}
