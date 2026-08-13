package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapSchema applies the auth-service schema required to run against a
// fresh identity database.
//
// Module 3 CLB-3 — THIS SPLITTER USED TO BE `strings.Split(sql, ";")`.
//
// That is only correct for SQL with no semicolon anywhere except at the end of
// a statement, and database/setup.sql has one inside a comment:
//
//	-- public key + sign_count are used to verify assertions at login; credential_id
//
// The naive split cut the file there, leaving the fragment ` credential_id\n--
// is the authenticator's handle (unique). ...` as a "statement". PostgreSQL
// answered 42601, BootstrapSchema returned the error, and main.go exits 1 on
// it — so auth-service could not start against a fresh database at all, and
// every table declared after that line (including the LB-5 consent record and
// the CLB-3 verification transactions) was never created.
//
// It was invisible because every environment already had the tables from an
// earlier revision of the file, so the bootstrap never ran on an empty
// database except during the CLB-3 live journey, which is what surfaced it.
//
// splitSQLStatements below therefore understands the four places a semicolon
// is not a statement terminator: line comments, block comments, quoted
// strings/identifiers, and dollar-quoted bodies.
func BootstrapSchema(ctx context.Context, db *pgxpool.Pool, schemaSQL string) error {
	if db == nil {
		return fmt.Errorf("db pool is nil")
	}
	if strings.TrimSpace(schemaSQL) == "" {
		return fmt.Errorf("schema sql is empty")
	}

	for idx, statement := range splitSQLStatements(schemaSQL) {
		if _, err := db.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply auth schema statement %d (%s): %w",
				idx+1, firstLine(statement), err)
		}
	}
	return nil
}

// firstLine gives an error message something to point at other than an index.
func firstLine(statement string) string {
	line := statement
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if len(line) > 80 {
		line = line[:80] + "…"
	}
	return line
}

// splitSQLStatements splits on statement-terminating semicolons only.
//
// It returns statements with comments preserved (PostgreSQL accepts them) but
// skips anything that is only whitespace and comments, so a trailing comment
// block does not become an empty Exec.
func splitSQLStatements(sql string) []string {
	var (
		out     []string
		current strings.Builder
	)
	runes := []rune(sql)

	flush := func() {
		if s := strings.TrimSpace(current.String()); hasExecutableSQL(s) {
			out = append(out, s)
		}
		current.Reset()
	}

	for i := 0; i < len(runes); {
		c := runes[i]

		switch {
		// -- line comment: runs to end of line.
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				current.WriteRune(runes[i])
				i++
			}

		// /* block comment */ — nestable in PostgreSQL.
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			depth := 0
			for i < len(runes) {
				if runes[i] == '/' && i+1 < len(runes) && runes[i+1] == '*' {
					depth++
					current.WriteRune(runes[i])
					current.WriteRune(runes[i+1])
					i += 2
					continue
				}
				if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '/' {
					depth--
					current.WriteRune(runes[i])
					current.WriteRune(runes[i+1])
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				current.WriteRune(runes[i])
				i++
			}

		// 'string literal' — '' is an escaped quote, not a terminator.
		case c == '\'':
			current.WriteRune(runes[i])
			i++
			for i < len(runes) {
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' {
						current.WriteRune(runes[i])
						current.WriteRune(runes[i+1])
						i += 2
						continue
					}
					current.WriteRune(runes[i])
					i++
					break
				}
				current.WriteRune(runes[i])
				i++
			}

		// "quoted identifier"
		case c == '"':
			current.WriteRune(runes[i])
			i++
			for i < len(runes) {
				if runes[i] == '"' {
					if i+1 < len(runes) && runes[i+1] == '"' {
						current.WriteRune(runes[i])
						current.WriteRune(runes[i+1])
						i += 2
						continue
					}
					current.WriteRune(runes[i])
					i++
					break
				}
				current.WriteRune(runes[i])
				i++
			}

		// $tag$ dollar-quoted body $tag$ — how function bodies are written,
		// and they are full of semicolons.
		case c == '$':
			if tag, ok := dollarTag(runes, i); ok {
				current.WriteString(tag)
				i += len([]rune(tag))
				for i < len(runes) {
					if strings.HasPrefix(string(runes[i:]), tag) {
						current.WriteString(tag)
						i += len([]rune(tag))
						break
					}
					current.WriteRune(runes[i])
					i++
				}
			} else {
				current.WriteRune(runes[i])
				i++
			}

		case c == ';':
			i++
			flush()

		default:
			current.WriteRune(runes[i])
			i++
		}
	}
	flush()
	return out
}

// dollarTag recognises $$ or $name$ at position i and returns it.
func dollarTag(runes []rune, i int) (string, bool) {
	if runes[i] != '$' {
		return "", false
	}
	for j := i + 1; j < len(runes); j++ {
		if runes[j] == '$' {
			return string(runes[i : j+1]), true
		}
		if !isTagRune(runes[j]) {
			return "", false
		}
	}
	return "", false
}

func isTagRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// hasExecutableSQL reports whether a chunk is more than whitespace and
// comments. A trailing comment block would otherwise be sent to the server as
// an empty statement.
func hasExecutableSQL(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		return true
	}
	return false
}
