package itest

import (
	"context"
	"strings"
	"testing"

	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

func TestPartnerSkillCategoriesUseActiveDatabaseCatalogue(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	s := store.New(p)
	code := "category_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := p.Exec(ctx, `INSERT INTO doorstep.skills(code,name) VALUES($1,'Category test')`, code); err != nil {
		t.Fatal(err)
	}
	active, inactive, onlyInactiveServices := uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{active, inactive, onlyInactiveServices} {
		if _, err := p.Exec(ctx, `INSERT INTO doorstep.categories(id,slug,name,family,active) VALUES($1,$2,$3,'APPLIANCE_REPAIR',$4)`, id, "category-test-"+id.String(), "Category "+id.String(), i != 1); err != nil {
			t.Fatal(err)
		}
		// Two services sharing a category must not duplicate that category.
		for j := 0; j < 2; j++ {
			if _, err := p.Exec(ctx, `INSERT INTO doorstep.services(category_id,slug,name,duration_minutes,required_skill,active) VALUES($1,$2,'Test service',30,$3,$4)`, id, "service-"+uuid.NewString(), code, i != 2); err != nil {
				t.Fatal(err)
			}
		}
	}
	items, err := s.ListSkills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range items {
		if skill.Code == code {
			if len(skill.Categories) != 1 || skill.Categories[0].ID != active || skill.Categories[0].Name != "Category "+active.String() {
				t.Fatalf("incorrect active categories: %+v", skill.Categories)
			}
			return
		}
	}
	t.Fatal("new database skill missing")
}
