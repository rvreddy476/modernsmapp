package store

import (
	"sort"
	"strings"
	"testing"
)

// The guard and the query must name the same columns.
//
// This is the drift that took the service down: GetSettings selected the full
// privacy matrix while SchemaRequirements asserted two columns, so a database
// missing eighteen of them passed the boot check and failed every settings
// request afterwards. Comparing the two lists here means adding a column to
// userSettingsColumns without adding it to the guard fails in CI rather than
// in production, where the symptom is graph-service quietly denying every
// direct message.
func TestSchemaRequirementsCoverUserSettingsColumns(t *testing.T) {
	queried := parseColumnList(userSettingsColumns)
	if len(queried) == 0 {
		t.Fatal("userSettingsColumns parsed to nothing; the parser is wrong, not the guard")
	}

	var asserted []string
	for _, req := range SchemaRequirements {
		if req.Table == "usr.user_settings" {
			asserted = append(asserted, req.Columns...)
		}
	}
	if len(asserted) == 0 {
		t.Fatal("no usr.user_settings requirement found")
	}

	sort.Strings(queried)
	sort.Strings(asserted)

	inGuard := make(map[string]bool, len(asserted))
	for _, c := range asserted {
		inGuard[c] = true
	}
	for _, c := range queried {
		if !inGuard[c] {
			t.Errorf("column %q is selected by userSettingsColumns but not asserted by "+
				"SchemaRequirements: a database missing it would pass the boot guard "+
				"and 42703 on the first settings read", c)
		}
	}

	inQuery := make(map[string]bool, len(queried))
	for _, c := range queried {
		inQuery[c] = true
	}
	for _, c := range asserted {
		if !inQuery[c] {
			t.Errorf("column %q is asserted by SchemaRequirements but no longer selected; "+
				"the guard would refuse a boot that should have succeeded", c)
		}
	}
}

// parseColumnList turns the SQL column list into individual names.
func parseColumnList(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}
