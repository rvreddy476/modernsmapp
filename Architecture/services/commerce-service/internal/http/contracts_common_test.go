package http

// Shared by the untagged inventory test and the integration-tagged renderer.

import (
	"path/filepath"
	"strconv"
	"strings"
)

// contractsDir is where the golden fixtures live, relative to this package.
const contractsDir = "testdata/contracts"

// ctStatusFromName reads the HTTP status a fixture's file name states:
// `<area>/<route>_<status>[_<case>]`. 0 when the name carries none.
func ctStatusFromName(name string) int {
	base := filepath.Base(name)
	for _, part := range strings.Split(base, "_") {
		if n, err := strconv.Atoi(part); err == nil && n >= 100 && n <= 599 {
			return n
		}
	}
	return 0
}
