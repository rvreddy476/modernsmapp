package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Audit L5: IsCeleb fails safe — only "no row" means "not celeb".
func TestCelebAnswer(t *testing.T) {
	if got, err := celebAnswer(false, pgx.ErrNoRows); got || err != nil {
		t.Fatalf("no row: (%v, %v), want (false, nil)", got, err)
	}
	if got, err := celebAnswer(false, fmt.Errorf("wrapped: %w", pgx.ErrNoRows)); got || err != nil {
		t.Fatalf("wrapped no row: (%v, %v), want (false, nil)", got, err)
	}
	if _, err := celebAnswer(false, errors.New("conn reset")); err == nil {
		t.Fatal("a database error was read as 'not celeb'")
	}
	if got, err := celebAnswer(true, nil); !got || err != nil {
		t.Fatalf("celeb row: (%v, %v), want (true, nil)", got, err)
	}
}
