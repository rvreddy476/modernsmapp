package scylla

import (
	"context"
	"fmt"
	"strings"
)

// messageColumn is a column added to chatservice.messages after the table
// first shipped.
type messageColumn struct {
	name    string
	cqlType string
}

// lateMessageColumns lists every column the message queries name that an
// older cluster may lack. scylla/schema.cql creates the table with all of
// them, but `CREATE TABLE IF NOT EXISTS` never alters an existing table, and
// until now each addition was a hand-run ALTER noted in a schema comment. A
// cluster that missed one fails EVERY send and read, because the INSERT and
// SELECTs name the column — so the service converges the table at boot.
var lateMessageColumns = []messageColumn{
	{"reply_to_id", "uuid"},
	{"reply_to_preview", "text"},
	{"reply_to_sender_id", "uuid"},
	{"is_edited", "boolean"},
	{"edited_at", "timestamp"},
	// Post cards (TikTok parity T-25, 2026-10-14).
	{"post_id", "uuid"},
}

// missingMessageColumns returns the late columns absent from existing.
func missingMessageColumns(existing map[string]bool) []messageColumn {
	var out []messageColumn
	for _, c := range lateMessageColumns {
		if !existing[c.name] {
			out = append(out, c)
		}
	}
	return out
}

// EnsureMessageColumns adds any late column chatservice.messages is missing
// and returns the names it added. Additive only: it never drops or retypes a
// column. A column another replica added concurrently counts as present.
func (s *MessageStore) EnsureMessageColumns(ctx context.Context, keyspace string) ([]string, error) {
	existing := map[string]bool{}
	iter := s.session.Query(
		`SELECT column_name FROM system_schema.columns WHERE keyspace_name = ? AND table_name = 'messages'`,
		keyspace,
	).WithContext(ctx).Iter()
	var name string
	for iter.Scan(&name) {
		existing[name] = true
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("read messages columns: %w", err)
	}
	if len(existing) == 0 {
		return nil, fmt.Errorf("table %s.messages not found", keyspace)
	}
	var added []string
	for _, c := range missingMessageColumns(existing) {
		stmt := fmt.Sprintf("ALTER TABLE messages ADD %s %s", c.name, c.cqlType)
		if err := s.session.Query(stmt).WithContext(ctx).Exec(); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "conflicts with an existing column") || strings.Contains(msg, "already exist") {
				continue
			}
			return added, fmt.Errorf("add messages.%s: %w", c.name, err)
		}
		added = append(added, c.name)
	}
	return added, nil
}
