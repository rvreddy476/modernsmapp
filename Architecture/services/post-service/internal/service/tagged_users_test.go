package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeTaggedUsers(t *testing.T) {
	author := uuid.New()
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	t.Run("nil and empty become an empty non-nil list", func(t *testing.T) {
		for _, in := range [][]uuid.UUID{nil, {}} {
			got, err := NormalizeTaggedUsers(author, in)
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if got == nil || len(got) != 0 {
				t.Fatalf("got %v, want empty non-nil slice", got)
			}
		}
	})

	t.Run("order is kept and duplicates collapse", func(t *testing.T) {
		got, err := NormalizeTaggedUsers(author, []uuid.UUID{b, a, b, c, a})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		want := []uuid.UUID{b, a, c}
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	})

	t.Run("the author and the nil id are dropped, not rejected", func(t *testing.T) {
		got, err := NormalizeTaggedUsers(author, []uuid.UUID{author, uuid.Nil, a})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if len(got) != 1 || got[0] != a {
			t.Fatalf("got %v want [%s]", got, a)
		}
	})

	t.Run("exactly the cap is accepted", func(t *testing.T) {
		ids := make([]uuid.UUID, MaxTaggedUsers)
		for i := range ids {
			ids[i] = uuid.New()
		}
		if _, err := NormalizeTaggedUsers(author, ids); err != nil {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("the cap counts the raw list, before dedupe", func(t *testing.T) {
		ids := make([]uuid.UUID, MaxTaggedUsers+1)
		for i := range ids {
			ids[i] = a
		}
		_, err := NormalizeTaggedUsers(author, ids)
		if !errors.Is(err, ErrTooManyTaggedUsers) {
			t.Fatalf("err=%v want ErrTooManyTaggedUsers", err)
		}
	})
}
