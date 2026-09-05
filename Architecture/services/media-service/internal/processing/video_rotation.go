package processing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Display rotation (Tube thumbnail sideways, 2026-09-05).
//
// A container can say "rotate this picture before you show it". Phones do it
// on every recording: the sensor is landscape, so a clip shot upright is a
// 1920x1080 coded frame plus a 90-degree display matrix. ffmpeg autorotates
// by default, so the thumbnail, the MP4 renditions and the HLS ladder all
// came out upright — but ProbeVideo read the coded size and stored it, so
// the asset said "1920 wide, 1080 tall" about pixels that were 1080 wide.
// post-service turned that into orientation=landscape and the client drew
// the (upright) picture into a landscape box, or vice versa.
//
// ffprobe reports the rotation as stream side data ("rotation=-90", degrees
// counter-clockwise) and, for files written by older muxers, as a "rotate"
// stream tag (degrees clockwise). Both are read; the side data wins.

// parseProbeDimensions reads the `-of default=noprint_wrappers=1` output of
//
//	ffprobe -show_entries stream=width,height:stream_side_data=rotation:stream_tags=rotate
//
// and returns the coded size and the display rotation in degrees
// counter-clockwise, normalised to 0, 90, 180 or 270.
func parseProbeDimensions(out string) (width, height, rotation int) {
	sideData, hasSideData := 0, false
	tag, hasTag := 0, false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "width":
			width, _ = strconv.Atoi(value)
		case "height":
			height, _ = strconv.Atoi(value)
		case "rotation":
			// Side data: counter-clockwise, may be negative or fractional
			// ("-90.00" from some muxers).
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				sideData, hasSideData = int(f), true
			}
		case "TAG:rotate":
			// Legacy tag: clockwise.
			if v, err := strconv.Atoi(value); err == nil {
				tag, hasTag = -v, true
			}
		}
	}
	switch {
	case hasSideData:
		rotation = normalizeRotation(sideData)
	case hasTag:
		rotation = normalizeRotation(tag)
	}
	return width, height, rotation
}

// normalizeRotation folds any number of degrees onto {0, 90, 180, 270}.
// Anything that is not a quarter turn is treated as no rotation: players
// only honour quarter turns, and swapping the dimensions for 45 degrees
// would describe nothing that exists.
func normalizeRotation(degrees int) int {
	r := degrees % 360
	if r < 0 {
		r += 360
	}
	switch r {
	case 0, 90, 180, 270:
		return r
	}
	return 0
}

// displayDimensions applies a display rotation to a coded size: a quarter
// turn swaps width and height, a half turn or none leaves them alone.
func displayDimensions(width, height, rotation int) (int, int) {
	switch normalizeRotation(rotation) {
	case 90, 270:
		return height, width
	}
	return width, height
}

// ValidRotationOverride reports whether degrees is a quarter turn an
// operator may ask the worker to stamp onto a file (see ApplyDisplayRotation).
// 0 is valid and means "reprocess with the file's own metadata".
func ValidRotationOverride(degrees int) bool {
	r := degrees % 360
	if r < 0 {
		r += 360
	}
	return r%90 == 0
}

// ApplyDisplayRotation remuxes inputPath to outputPath with a display
// rotation of degreesCCW stamped on the video stream. Stream copy: no
// re-encode, no quality loss, seconds of work.
//
// This is the repair for a file whose pixels are sideways and which carries
// NO rotation metadata — the founder's "Family Outing" upload was a 478x850
// coded frame holding a landscape scene on its side, with nothing in the
// container to say so. No probe can recover that; an operator can. Once the
// matrix is in the file, every existing path (ProbeVideo, thumbnail, MP4,
// HLS, and a player opening the original) does the right thing on its own.
func ApplyDisplayRotation(ctx context.Context, inputPath, outputPath string, degreesCCW int) error {
	if !ValidRotationOverride(degreesCCW) {
		return fmt.Errorf("rotation override must be a multiple of 90 degrees, got %d", degreesCCW)
	}
	args := []string{
		"-y",
		"-display_rotation", strconv.Itoa(normalizeRotation(degreesCCW)),
		"-i", inputPath,
		"-map", "0",
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg display_rotation remux: %w", err)
	}
	return nil
}
