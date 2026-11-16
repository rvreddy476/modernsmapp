package scylla

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// feed-08: the rows of a deleted post are removed through a bounded pool,
// not one after another, and a failed row still fails the delete.

func indexedRows(n int, kind string) []indexedRow {
	rows := make([]indexedRow, n)
	for i := range rows {
		rows[i] = indexedRow{kind: kind, owner: gocql.UUID(uuid.New()), b: 202611, ts: gocql.TimeUUID()}
	}
	return rows
}

func TestDeleteIndexedRows_RunsConcurrentlyAndBounded(t *testing.T) {
	rows := indexedRows(50, "home")
	var inFlight, peak atomic.Int32
	start := time.Now()
	owners, err := deleteIndexedRows(rows, func(indexedRow) (bool, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		return true, nil
	})
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	// One after another this is 500 ms; sixteen at a time it is four rounds.
	if took >= 250*time.Millisecond {
		t.Fatalf("50 deletes of 10 ms took %v; they ran one after another", took)
	}
	if p := peak.Load(); p > timelineDeleteWorkers || p < 2 {
		t.Fatalf("peak concurrency %d, want between 2 and %d", p, timelineDeleteWorkers)
	}
	if len(owners) != len(rows) {
		t.Fatalf("%d owners returned, want %d", len(owners), len(rows))
	}
	for i, o := range owners {
		if o.OwnerID != uuid.UUID(rows[i].owner) || o.Kind != "home" {
			t.Fatalf("owner %d is out of row order", i)
		}
	}
}

func TestDeleteIndexedRows_OneFailedRowFailsTheDelete(t *testing.T) {
	rows := indexedRows(40, "home")
	boom := errors.New("scylla write timeout")
	var mu sync.Mutex
	attempted := 0
	owners, err := deleteIndexedRows(rows, func(r indexedRow) (bool, error) {
		mu.Lock()
		attempted++
		mu.Unlock()
		if r == rows[7] {
			return true, boom
		}
		return true, nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v; a failed row was swallowed, so the index would be dropped and the row orphaned", err)
	}
	if attempted != len(rows) {
		t.Fatalf("%d of %d rows attempted; the rest are left for the retry for no reason", attempted, len(rows))
	}
	if len(owners) != len(rows)-1 {
		t.Fatalf("%d owners returned, want %d (all but the failed row)", len(owners), len(rows)-1)
	}
	for _, o := range owners {
		if o.OwnerID == uuid.UUID(rows[7].owner) {
			t.Fatal("the failed row's owner was reported as deleted")
		}
	}
}

func TestDeleteIndexedRows_UnknownKindsAreSkippedAndEmptyIsFine(t *testing.T) {
	rows := append(indexedRows(2, "home"), indexedRows(1, "mystery")...)
	owners, err := deleteIndexedRows(rows, func(r indexedRow) (bool, error) { return r.kind == "home", nil })
	if err != nil || len(owners) != 2 {
		t.Fatalf("owners %d, err %v; want the 2 home rows and no error", len(owners), err)
	}
	owners, err = deleteIndexedRows(nil, func(indexedRow) (bool, error) {
		t.Fatal("called for no rows")
		return false, nil
	})
	if err != nil || owners == nil || len(owners) != 0 {
		t.Fatalf("no rows: owners %v, err %v", owners, err)
	}
}
