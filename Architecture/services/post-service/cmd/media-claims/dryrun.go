package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// dryRunOnly stands in for the client on a dry run without credentials. A
// dry run never calls it; if it ever did, it refuses rather than pretend.
type dryRunOnly struct{}

var errNoClient = errors.New("media-claims: no media-service client (dry run only)")

func (dryRunOnly) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error   { return errNoClient }
func (dryRunOnly) Release(context.Context, string, uuid.UUID, []uuid.UUID) error { return errNoClient }
func (dryRunOnly) ReleaseExcept(context.Context, string, uuid.UUID, []uuid.UUID) error {
	return errNoClient
}
