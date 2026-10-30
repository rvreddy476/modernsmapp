//go:build !unix

package main

// scratchSpaceOf is unknown off unix (a developer's Windows build); the
// download goes ahead and the size cap still applies.
func scratchSpaceOf(string) (scratchSpace, bool) { return scratchSpace{}, false }
