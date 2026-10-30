//go:build unix

package main

import "syscall"

// scratchSpaceOf reads the free and total bytes of the filesystem holding
// dir. ok is false when it cannot be read; the download then goes ahead
// and a full disk surfaces as ENOSPC.
func scratchSpaceOf(dir string) (scratchSpace, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return scratchSpace{}, false
	}
	bs := int64(st.Bsize)                                                                //nolint:unconvert // Bsize is int32 on some unix targets
	return scratchSpace{free: int64(st.Bavail) * bs, total: int64(st.Blocks) * bs}, true //nolint:gosec // G115: block counts of a local filesystem fit int64
}
