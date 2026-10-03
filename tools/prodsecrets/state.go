package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Local state: key ids and timestamps ONLY.
//
// state.json answers "when was this key created / last rotated" and nothing
// else. It never holds a value, a hash of a value or a length: losing it or
// leaking it costs nothing. Idempotency comes from what Secrets Manager
// already holds (out/current, fetched before every apply) and from payloads
// an earlier apply wrote but the lead has not pushed yet (out/secrets).

// KeyRecord is the history of one "<secret>/<key>" or "role/<role>".
type KeyRecord struct {
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Rotations int       `json:"rotations"`
}

// RunRecord is one apply.
type RunRecord struct {
	At      time.Time `json:"at"`
	Created int       `json:"created"`
	Updated int       `json:"updated"`
	Kept    int       `json:"kept"`
	Reused  int       `json:"reused"`
	Pending int       `json:"pending"`
	Rotate  []string  `json:"rotate,omitempty"` // the --rotate targets (names, not values)
}

// State is out/state.json.
type State struct {
	Version int                  `json:"version"`
	Keys    map[string]KeyRecord `json:"keys"`
	Runs    []RunRecord          `json:"runs"`
}

// NewState returns an empty state.
func NewState() *State {
	return &State{Version: 2, Keys: map[string]KeyRecord{}}
}

// Record folds an applied plan into the state.
func (s *State) Record(p *Plan, rotate []string, now time.Time) {
	run := RunRecord{At: now, Rotate: rotate}
	touch := func(id, kind string, rotated bool) {
		e, exists := s.Keys[id]
		if !exists {
			e = KeyRecord{Kind: kind, CreatedAt: now}
		} else if rotated {
			e.Rotations++
		}
		e.Kind = kind
		e.UpdatedAt = now
		s.Keys[id] = e
	}
	for _, sp := range p.Secrets {
		for _, k := range sp.Keys {
			id := sp.Name + "/" + k.Key
			switch k.Action {
			case ActCreate:
				run.Created++
				touch(id, k.Spec.String(), false)
			case ActUpdate:
				run.Updated++
				touch(id, k.Spec.String(), true)
			case ActReuse:
				run.Reused++
				if _, ok := s.Keys[id]; !ok {
					touch(id, k.Spec.String(), false)
				}
			case ActKeep:
				run.Kept++
				if _, ok := s.Keys[id]; !ok {
					// Filled before this tool's first run.
					touch(id, k.Spec.String(), false)
				}
			case ActPending:
				run.Pending++
			}
		}
	}
	for _, g := range p.Roles {
		id := "role/" + g.Role
		if _, ok := s.Keys[id]; !ok {
			touch(id, "postgres-role", false)
		} else if p.RotatedRoles[g.Role] {
			touch(id, "postgres-role", true)
		}
	}
	s.Runs = append(s.Runs, run)
}

// IDs returns the recorded ids, sorted.
func (s *State) IDs() []string {
	out := make([]string, 0, len(s.Keys))
	for id := range s.Keys {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// LoadState reads out/state.json; a missing file is an empty state.
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return nil, err
	}
	st := NewState()
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	if st.Version != 2 {
		return nil, fmt.Errorf("state %s: version %d is from an older prodsecrets that stored values; delete it (Secrets Manager is the source of truth)", path, st.Version)
	}
	if st.Keys == nil {
		st.Keys = map[string]KeyRecord{}
	}
	return st, nil
}

// SaveState writes the state atomically with mode 0600.
func SaveState(path string, st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, append(b, '\n'))
}

// writePrivate writes a file readable only by the owner, atomically.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// os.Rename replaces atomically on POSIX; on Windows it also replaces an
	// existing file since Go 1.5.
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Value inputs. All three are directories of <name>.json flat JSON objects
// with mode 0600; scripts/prodsecrets.sh writes them and removes current/
// and sources/ again when the run ends.

// LoadValueDir reads every <name>.json in dir into name -> key -> value.
// A missing directory is (nil, false, nil) so callers can tell "not fetched"
// from "fetched and empty".
func LoadValueDir(dir string) (map[string]map[string]string, bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]map[string]string{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := map[string]map[string]string{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, true, err
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if len(strings.TrimSpace(string(b))) == 0 {
			out[name] = map[string]string{}
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			// Never include the content in the error: it is a secret.
			return nil, true, fmt.Errorf("%s: not a JSON object", filepath.Join(dir, e.Name()))
		}
		flat := make(map[string]string, len(m))
		for k, v := range m {
			flat[k] = stringify(v)
		}
		out[name] = flat
	}
	return out, true, nil
}

// LoadTFOutputs reads a Terraform outputs file. Both `terraform output -json`
// ({"name": {"value": ..., "sensitive": ...}}) and a flat {"name": value}
// object are accepted; lists are joined with commas, maps kept as JSON.
func LoadTFOutputs(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("terraform outputs %s: not a JSON object", path)
	}
	for k, v := range raw {
		if obj, ok := v.(map[string]any); ok {
			if inner, ok := obj["value"]; ok && len(obj) <= 3 {
				v = inner
			}
		}
		out[k] = stringify(v)
	}
	return out, nil
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			parts = append(parts, stringify(p))
		}
		return strings.Join(parts, ",")
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// UnversionedIn lists the <name>.json files in dir that are EMPTY (0 bytes):
// scripts/prodsecrets.sh writes that for a secret Secrets Manager holds no
// version of yet, and "{}" for a secret whose current version is an empty
// object. Only the first kind needs a first version pushed.
func UnversionedIn(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.Size() == 0 {
			out[strings.TrimSuffix(e.Name(), ".json")] = true
		}
	}
	return out
}
