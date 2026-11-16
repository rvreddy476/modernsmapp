package scylla

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// timelineDeleteWorkers bounds the concurrent row deletes of one post
// delete. Sixteen, as the fan-out's write pool (service.fanoutWorkers): the
// two share a Scylla session and a consumer.
const timelineDeleteWorkers = 16

// deleteIndexedRows runs del once for every row through a bounded worker
// pool and waits for all of them. del reports whether the row was one it
// deletes (an unknown timeline kind is skipped, not an error).
//
// It returns the owners whose row was deleted, in row order, and an error
// if ANY delete failed. Every row is attempted even after a failure, so a
// retry has as little left to do as possible; the deletes are idempotent,
// so rows deleted before a failure are simply deleted again.
func deleteIndexedRows(rows []indexedRow, del func(indexedRow) (bool, error)) ([]TimelineOwner, error) {
	if len(rows) == 0 {
		return []TimelineOwner{}, nil
	}
	deleted := make([]bool, len(rows))
	errs := make([]error, len(rows))
	next := make(chan int)
	var wg sync.WaitGroup
	workers := min(timelineDeleteWorkers, len(rows))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				ok, err := del(rows[i])
				deleted[i], errs[i] = ok && err == nil, err
			}
		}()
	}
	for i := range rows {
		next <- i
	}
	close(next)
	wg.Wait()

	owners := make([]TimelineOwner, 0, len(rows))
	var first error
	failed := 0
	for i, r := range rows {
		if errs[i] != nil {
			failed++
			if first == nil {
				first = errs[i]
			}
			continue
		}
		if deleted[i] {
			owners = append(owners, TimelineOwner{Kind: r.kind, OwnerID: uuid.UUID(r.owner)})
		}
	}
	if first != nil {
		return owners, fmt.Errorf("%d of %d timeline row delete(s) failed: %w", failed, len(rows), first)
	}
	return owners, nil
}
