package scylla

import (
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// gocql marshals a CQL `uuid` column from its own gocql.UUID type, from
// a string, or from a []byte — but NOT from github.com/google/uuid.UUID,
// even though both are [16]byte underneath. Passing one straight through
// fails at bind time with:
//
//	can not marshal uuid.UUID into uuid
//
// Every query in this package bound google/uuid values directly, so
// every write silently warned and every read returned an error: the
// watch_sessions, viewer_history and reel_views tables stayed empty on a
// running stack, and the retention / audience-demographics endpoints
// answered 500 because their iterator close returned this marshal error.
//
// cql/gouuid are the conversion at the driver boundary. They are free
// (a type conversion between identical array types), and keeping them
// in one place means a new query cannot reintroduce the bug by writing
// the obvious thing.
func cql(id uuid.UUID) gocql.UUID { return gocql.UUID(id) }

func gouuid(id gocql.UUID) uuid.UUID { return uuid.UUID(id) }
