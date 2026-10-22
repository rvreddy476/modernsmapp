package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeDB records every statement and its arguments, and answers reads from
// canned rows. It is how the field-encryption tests prove what actually
// reaches PostgreSQL.
type fakeDB struct {
	mu    sync.Mutex
	calls []dbCall
	// rows answers Query / QueryRow by SQL. nil rows for QueryRow is
	// pgx.ErrNoRows.
	rows func(sql string, args []any) [][]any
}

type dbCall struct {
	kind string // exec | query | queryrow
	sql  string
	args []any
}

func (f *fakeDB) record(kind, sql string, args []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dbCall{kind, sql, append([]any(nil), args...)})
}

func (f *fakeDB) answer(sql string, args []any) [][]any {
	if f.rows == nil {
		return nil
	}
	return f.rows(sql, args)
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.record("exec", sql, args)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (f *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.record("query", sql, args)
	return &fakeRows{data: f.answer(sql, args)}, nil
}

func (f *fakeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.record("queryrow", sql, args)
	data := f.answer(sql, args)
	if len(data) == 0 {
		return fakeRow{err: pgx.ErrNoRows}
	}
	return fakeRow{vals: data[0]}
}

func (f *fakeDB) Begin(context.Context) (pgx.Tx, error) { return &fakeTx{db: f}, nil }

// writes returns the exec calls and the args of every call.
func (f *fakeDB) writes() []dbCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dbCall
	for _, c := range f.calls {
		if c.kind == "exec" || strings.Contains(strings.ToUpper(c.sql), "INSERT") || strings.Contains(strings.ToUpper(c.sql), "UPDATE") {
			out = append(out, c)
		}
	}
	return out
}

type fakeTx struct {
	pgx.Tx
	db *fakeDB
}

func (t *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.db.Exec(ctx, sql, args...)
}
func (t *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.db.Query(ctx, sql, args...)
}
func (t *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.db.QueryRow(ctx, sql, args...)
}
func (t *fakeTx) Commit(context.Context) error   { return nil }
func (t *fakeTx) Rollback(context.Context) error { return nil }

type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanInto(r.vals, dest)
}

type fakeRows struct {
	data [][]any
	i    int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT") }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool {
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}
func (r *fakeRows) Scan(dest ...any) error { return scanInto(r.data[r.i-1], dest) }
func (r *fakeRows) Values() ([]any, error) { return r.data[r.i-1], nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *pgx.Conn        { return nil }

func scanInto(vals []any, dest []any) error {
	if len(vals) != len(dest) {
		return fmt.Errorf("fake row has %d values, scan has %d targets", len(vals), len(dest))
	}
	for i := range dest {
		if err := assign(dest[i], vals[i]); err != nil {
			return fmt.Errorf("column %d: %w", i, err)
		}
	}
	return nil
}

func assign(dest, v any) error {
	dv := reflect.ValueOf(dest).Elem()
	if v == nil {
		dv.Set(reflect.Zero(dv.Type()))
		return nil
	}
	vv := reflect.ValueOf(v)
	set := func(target reflect.Value) bool {
		switch {
		case vv.Type().AssignableTo(target.Type()):
			target.Set(vv)
		case vv.Type().ConvertibleTo(target.Type()):
			target.Set(vv.Convert(target.Type()))
		default:
			return false
		}
		return true
	}
	if set(dv) {
		return nil
	}
	if dv.Kind() == reflect.Pointer {
		p := reflect.New(dv.Type().Elem())
		if set(p.Elem()) {
			dv.Set(p)
			return nil
		}
	}
	return fmt.Errorf("cannot scan %T into %s", v, dv.Type())
}

// argText renders an argument for plaintext searches.
func argText(a any) string {
	switch v := a.(type) {
	case nil:
		return ""
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	case []byte:
		return string(v)
	case json.RawMessage:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
