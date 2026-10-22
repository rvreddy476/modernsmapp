package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Security sweep #16: SendTip asks graph for a block in either direction
// BEFORE the cap query, the wallets or the ledger. The store is nil in these
// tests, so a tip that got past the gate panics on its first query: a clean
// refusal proves the order.

type fakeTipBlocks struct {
	blocked bool
	err     error
	asked   [][2]uuid.UUID
}

func (f *fakeTipBlocks) BlockedEitherWay(_ context.Context, a, b uuid.UUID) (bool, error) {
	f.asked = append(f.asked, [2]uuid.UUID{a, b})
	return f.blocked, f.err
}

func tipInput() SendTipInput {
	return SendTipInput{SenderID: uuid.New(), RecipientID: uuid.New(), AmountPaise: 500, Message: "thanks"}
}

func TestTipAcrossABlockIsRefusedBeforeTheLedger(t *testing.T) {
	blocks := &fakeTipBlocks{blocked: true}
	s := (&Service{}).WithTipBlockChecker(blocks)
	in := tipInput()
	_, err := s.SendTip(context.Background(), in)
	if !errors.Is(err, ErrTipNotAllowed) {
		t.Fatalf("blocked pair: %v, want ErrTipNotAllowed", err)
	}
	if len(blocks.asked) != 1 || blocks.asked[0] != [2]uuid.UUID{in.SenderID, in.RecipientID} {
		t.Fatalf("graph asked %v, want once about (sender, recipient)", blocks.asked)
	}
	// The refusal must not tell the sender why.
	if strings.Contains(strings.ToLower(err.Error()), "block") {
		t.Fatalf("the refusal names the block: %v", err)
	}
}

func TestTipFailsClosedWhenTheBlockLookupDoesNotAnswer(t *testing.T) {
	for name, s := range map[string]*Service{
		"graph error":    (&Service{}).WithTipBlockChecker(&fakeTipBlocks{err: errors.New("graph down")}),
		"not configured": {},
	} {
		if _, err := s.SendTip(context.Background(), tipInput()); !errors.Is(err, ErrTipUnavailable) {
			t.Fatalf("%s: %v, want ErrTipUnavailable", name, err)
		}
	}
}

// Not blocked: the tip goes on to the ledger — here, the nil store's panic.
func TestTipWithoutABlockReachesTheLedger(t *testing.T) {
	s := (&Service{}).WithTipBlockChecker(&fakeTipBlocks{})
	reached := func() (reached bool) {
		defer func() {
			if recover() != nil {
				reached = true
			}
		}()
		_, _ = s.SendTip(context.Background(), tipInput())
		return false
	}()
	if !reached {
		t.Fatal("an unblocked tip never reached the store")
	}
}

// Validation still answers first: a self-tip never costs a graph call.
func TestTipValidationRunsBeforeTheBlockLookup(t *testing.T) {
	blocks := &fakeTipBlocks{}
	s := (&Service{}).WithTipBlockChecker(blocks)
	id := uuid.New()
	if _, err := s.SendTip(context.Background(), SendTipInput{SenderID: id, RecipientID: id, AmountPaise: 500}); err == nil || !strings.HasPrefix(err.Error(), "CANNOT_TIP_SELF") {
		t.Fatalf("self-tip: %v", err)
	}
	if len(blocks.asked) != 0 {
		t.Fatalf("graph asked about an invalid tip")
	}
}
