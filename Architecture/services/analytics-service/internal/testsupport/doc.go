// Package testsupport holds the fixtures the integration suite needs.
// Everything in it is behind the `integration` build tag; this file
// exists only so `go build ./...` and `go vet ./...` without that tag
// see a package rather than "build constraints exclude all Go files".
package testsupport
