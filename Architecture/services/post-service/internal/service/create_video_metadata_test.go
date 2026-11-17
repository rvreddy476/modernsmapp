package service

import (
	"os"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// reels-1 (b): a video post's video_metadata row is written in the post's
// own create transaction. It used to be inserted after the commit and only
// logged on failure, leaving a post the transcode consumer could never find.

func TestBuildCreateVideoMetadata(t *testing.T) {
	post, media := uuid.New(), uuid.New()
	ready := map[uuid.UUID]postgres.MediaMetadata{media: {Kind: "video", ProcessingStatus: mediaReady}}

	vm := buildCreateVideoMetadata(post, "flick", media, ready, 30, 1080, 1920)
	if vm.PostID != post || vm.MediaAssetID == nil || *vm.MediaAssetID != media {
		t.Fatalf("row does not join the post to its media: %+v", vm)
	}
	if vm.UploadStatus != mediaReady || vm.FinalCategory != "flick" || vm.DurationSeconds != 30 ||
		vm.Width == nil || *vm.Width != 1080 || vm.Orientation == "" {
		t.Fatalf("measured ready media: %+v", vm)
	}
	// Still processing, nothing measured: pending, provisional answer.
	vm = buildCreateVideoMetadata(post, "flick", media, nil, 0, 0, 0)
	if vm.UploadStatus != "pending" || vm.ComputedCategory != "flick" || vm.Orientation != "portrait" || vm.Width != nil {
		t.Fatalf("unmeasured media: %+v", vm)
	}
}

// Held by source, as the consumer's attachment rule is: CreatePost hands the
// row to the create write and no longer inserts it after the commit.
func TestCreatePostWritesVideoMetadataInTheCreateTransaction(t *testing.T) {
	src, err := os.ReadFile("post_create.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	if !strings.Contains(code, "p.VideoMetadataWrite = buildCreateVideoMetadata(") {
		t.Fatal("CreatePost no longer hands video_metadata to the create write")
	}
	if strings.Contains(code, ".CreateVideoMetadata(") {
		t.Fatal("CreatePost inserts video_metadata outside the create transaction again")
	}
	store, err := os.ReadFile("../store/postgres/posts.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(store), "insertVideoMetadata(ctx, tx, p.VideoMetadataWrite)") {
		t.Fatal("the create transaction does not insert VideoMetadataWrite")
	}
}
