package processing

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// FFmpeg thread count (long-video audit lv-13, 2026-11-17).
//
// ffmpeg's default (-threads 0, auto) sizes x264's thread pool from the
// CPUs the KERNEL reports, which inside a container is the node's count,
// not the pod's CPU limit. A 3-CPU worker on a 32-core node ran 32+ encoder
// threads (each with its own lookahead and frame buffers) inside a 4 Gi
// memory limit shared by the reel loop, the long loop and the fingerprint
// extractor. The worker now passes an explicit count:
//
//   - MEDIA_FFMPEG_THREADS, when set to a positive integer (operator override);
//   - otherwise runtime.GOMAXPROCS(0), which since Go 1.25 follows the
//     container's cgroup CPU limit (rounded up), so it is the CPU the pod may
//     actually use.

// ffmpegThreadsEnv overrides the thread count every encode is given.
const ffmpegThreadsEnv = "MEDIA_FFMPEG_THREADS"

// ffmpegThreadsFrom is the thread count for an environment and a GOMAXPROCS.
func ffmpegThreadsFrom(getenv func(string) string, maxProcs int) int {
	if v := strings.TrimSpace(getenv(ffmpegThreadsEnv)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if maxProcs < 1 {
		return 1
	}
	return maxProcs
}

// FFmpegThreads is the thread count every x264 encode the worker runs is
// given (see the file comment).
func FFmpegThreads() int {
	return ffmpegThreadsFrom(os.Getenv, runtime.GOMAXPROCS(0))
}

// threadArgs is the output option that pins the encoder's thread pool.
func threadArgs() []string {
	return []string{"-threads", strconv.Itoa(FFmpegThreads())}
}
