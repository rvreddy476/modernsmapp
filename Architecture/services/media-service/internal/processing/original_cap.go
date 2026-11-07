package processing

import (
	"log"
	"strconv"
	"strings"
)

// MaxVideoOriginalBytes is the largest video original the platform takes:
// 16 GiB. It is the ONE source of truth for both ends (2026-11-07): the
// upload routes refuse a larger video at init (413 VIDEO_TOO_LARGE), and the
// transcode worker refuses one before downloading it. Before, the resumable
// route accepted up to 625 GiB (S3's part arithmetic) and the worker then
// failed every original over 16 GiB after the user had uploaded all of it.
//
// 16 GiB is what the worker's scratch volume (50 Gi in prod) holds with the
// original plus its renditions and HLS ladder (about 3x the original).
const MaxVideoOriginalBytes int64 = 16 << 30

// VideoOriginalCapEnv overrides MaxVideoOriginalBytes for one environment
// (QA and staging run a 20 Gi scratch volume and set 6 GiB). It sits in the
// env block the server and the worker share, and both read it through
// VideoOriginalCap, so the upload routes and the worker cannot disagree.
const VideoOriginalCapEnv = "MEDIA_WORKER_MAX_ORIGINAL_BYTES"

// VideoOriginalCap is the cap in force: VideoOriginalCapEnv when it is a
// non-negative number of bytes, else MaxVideoOriginalBytes. 0 means no cap
// (the worker's long-standing meaning of the variable).
func VideoOriginalCap(getenv func(string) string) int64 {
	v := strings.TrimSpace(getenv(VideoOriginalCapEnv))
	if v == "" {
		return MaxVideoOriginalBytes
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > float64(1<<62) {
		log.Printf("%s=%q is not a non-negative number of bytes; using %d", VideoOriginalCapEnv, v, MaxVideoOriginalBytes)
		return MaxVideoOriginalBytes
	}
	return int64(f)
}
