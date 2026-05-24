package service

import (
	"testing"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// TestGroupOrdersForBatching covers the pure grouping algorithm.
//
//	r1 = restaurant A, r2 = restaurant B
//	t0 = anchor time
//
// Cases mirror what dispatchPendingOrders will see in production.
func TestGroupOrdersForBatching(t *testing.T) {
	r1 := uuid.New()
	r2 := uuid.New()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	type tcase struct {
		name        string
		in          []postgres.ReadyOrderForBatching
		wantGroups  int
		wantSizes   []int
	}

	cases := []tcase{
		{
			name:       "empty input -> nil",
			in:         nil,
			wantGroups: 0,
			wantSizes:  nil,
		},
		{
			name: "single order -> one singleton group",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
			},
			wantGroups: 1,
			wantSizes:  []int{1},
		},
		{
			name: "two same-restaurant orders within window -> one batch of 2",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(2 * time.Minute)},
			},
			wantGroups: 1,
			wantSizes:  []int{2},
		},
		{
			name: "three same-restaurant orders within window -> one batch of 3",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(1 * time.Minute)},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(3 * time.Minute)},
			},
			wantGroups: 1,
			wantSizes:  []int{3},
		},
		{
			name: "four same-restaurant orders -> batch of 3 + singleton (size cap)",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(1 * time.Minute)},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(2 * time.Minute)},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(3 * time.Minute)},
			},
			wantGroups: 2,
			wantSizes:  []int{3, 1},
		},
		{
			name: "two restaurants -> two singletons",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
				{OrderID: uuid.New(), RestaurantID: r2, PlacedAt: t0.Add(1 * time.Minute)},
			},
			wantGroups: 2,
			wantSizes:  []int{1, 1},
		},
		{
			name: "same restaurant outside window -> two singletons",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(6 * time.Minute)},
			},
			wantGroups: 2,
			wantSizes:  []int{1, 1},
		},
		{
			name: "mixed: two restaurants both batch eligible",
			in: []postgres.ReadyOrderForBatching{
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0},
				{OrderID: uuid.New(), RestaurantID: r1, PlacedAt: t0.Add(1 * time.Minute)},
				{OrderID: uuid.New(), RestaurantID: r2, PlacedAt: t0.Add(2 * time.Minute)},
				{OrderID: uuid.New(), RestaurantID: r2, PlacedAt: t0.Add(3 * time.Minute)},
			},
			wantGroups: 2,
			wantSizes:  []int{2, 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := groupOrdersForBatching(tc.in)
			if len(got) != tc.wantGroups {
				t.Fatalf("want %d groups, got %d", tc.wantGroups, len(got))
			}
			for i, g := range got {
				if len(g.orderIDs) != tc.wantSizes[i] {
					t.Errorf("group %d: want size %d, got %d", i, tc.wantSizes[i], len(g.orderIDs))
				}
			}
		})
	}
}

// TestGroupOrdersForBatching_AnchorRule confirms the window is measured
// from the anchor (first member's placed_at), not from the most recent
// member — so a long chain of 1-minute-apart orders eventually
// terminates the batch instead of growing indefinitely.
func TestGroupOrdersForBatching_AnchorRule(t *testing.T) {
	r := uuid.New()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// 6 orders at t0, t0+2m, t0+4m, t0+6m, t0+8m, t0+10m
	// anchor=t0; window=5m → first batch closes after t0+4m (anchor+4=4≤5)
	// then t0+6m starts a new anchor; +8m and +10m are inside the new
	// window. With size cap 3 the first batch is {t0, t0+2, t0+4}, the
	// second is {t0+6, t0+8, t0+10}.
	in := []postgres.ReadyOrderForBatching{
		{OrderID: uuid.New(), RestaurantID: r, PlacedAt: t0},
		{OrderID: uuid.New(), RestaurantID: r, PlacedAt: t0.Add(2 * time.Minute)},
		{OrderID: uuid.New(), RestaurantID: r, PlacedAt: t0.Add(4 * time.Minute)},
		{OrderID: uuid.New(), RestaurantID: r, PlacedAt: t0.Add(6 * time.Minute)},
		{OrderID: uuid.New(), RestaurantID: r, PlacedAt: t0.Add(8 * time.Minute)},
		{OrderID: uuid.New(), RestaurantID: r, PlacedAt: t0.Add(10 * time.Minute)},
	}
	got := groupOrdersForBatching(in)
	if len(got) != 2 {
		t.Fatalf("want 2 groups, got %d", len(got))
	}
	if len(got[0].orderIDs) != 3 || len(got[1].orderIDs) != 3 {
		t.Errorf("want 3+3, got %d+%d", len(got[0].orderIDs), len(got[1].orderIDs))
	}
}
