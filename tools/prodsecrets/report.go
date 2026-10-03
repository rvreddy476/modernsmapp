package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Redactor is the last line of defence: every byte the tool prints passes
// through it, and any known secret value is replaced. The report code never
// prints values on purpose; this makes an accidental print harmless too.
type Redactor struct {
	values []string
}

// NewRedactor collects every value the plan and the inputs know about.
func NewRedactor(p *Plan, in Inputs) *Redactor {
	seen := map[string]bool{}
	public := map[string]bool{}
	if p != nil {
		for _, pair := range p.PublicHalves {
			public[strings.TrimSpace(pair.Public)] = true
			for _, line := range strings.Split(pair.Public, "\n") {
				public[strings.TrimSpace(line)] = true
			}
		}
	}
	var vals []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) < 8 || seen[v] || public[v] {
			return
		}
		seen[v] = true
		vals = append(vals, v)
		// PEM bodies and JSON key files are often printed one line at a time.
		if strings.Contains(v, "\n") {
			for _, line := range strings.Split(v, "\n") {
				line = strings.TrimSpace(line)
				if len(line) >= 8 && !strings.HasPrefix(line, "-----") && !seen[line] && !public[line] {
					seen[line] = true
					vals = append(vals, line)
				}
			}
		}
	}
	if p != nil {
		for _, s := range p.Secrets {
			for _, k := range s.Keys {
				add(k.Value)
			}
		}
		for _, pw := range p.RolePasswords {
			add(pw)
		}
		for _, pair := range p.PublicHalves {
			add(pair.Private)
		}
	}
	for _, src := range []map[string]map[string]string{in.Current, in.Pending, in.Sources} {
		for _, keys := range src {
			for _, v := range keys {
				add(v)
			}
		}
	}
	// Longest first so a value that contains another is replaced whole.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	return &Redactor{values: vals}
}

// Redact replaces every known value in s.
func (r *Redactor) Redact(s string) string {
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	return s
}

// Writer returns an io.Writer that redacts before writing to w.
func (r *Redactor) Writer(w io.Writer) io.Writer {
	return redactWriter{r: r, w: w}
}

type redactWriter struct {
	r *Redactor
	w io.Writer
}

func (rw redactWriter) Write(p []byte) (int, error) {
	if _, err := rw.w.Write([]byte(rw.r.Redact(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// PrintPlan writes the human report: one line per key with its action and
// origin, then the summary and checklist. Values never appear.
func PrintPlan(w io.Writer, m *Manifest, p *Plan, apply bool, verbose bool) {
	mode := "PLAN (nothing written)"
	if apply {
		mode = "APPLY"
	}
	secrets, keys, byCategory := m.Summary()
	fmt.Fprintf(w, "prodsecrets %s — %d secrets, %d keys under %s/\n", mode, secrets, keys, m.Prefix)
	for _, c := range []string{"generate", "prompt", "terraform-output", "derive", "copy", "literal"} {
		fmt.Fprintf(w, "  %-18s %d\n", c, byCategory[c])
	}
	fmt.Fprintln(w)

	for _, s := range p.Secrets {
		counts := map[Action]int{}
		for _, k := range s.Keys {
			counts[k.Action]++
		}
		fmt.Fprintf(w, "%s  (%s)\n", s.FullName, summarise(counts))
		if len(s.Keys) == 0 {
			fmt.Fprintf(w, "    (no keys: pushed as {})\n")
		}
		for _, k := range s.Keys {
			if !verbose && k.Action == ActKeep {
				continue
			}
			spec := k.Spec.String()
			if k.Spec.IsPrompt() {
				spec = k.Spec.Kind
			}
			line := fmt.Sprintf("    %-8s %-36s %-28s %s", k.Action, k.Key, spec, k.Origin)
			if k.Reason != "" {
				line += "  [" + k.Reason + "]"
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %s\n", summarise(p.Counts()))
	if len(p.Roles) > 0 {
		dbs := map[string]int{}
		for _, g := range p.Roles {
			dbs[g.Database]++
		}
		names := make([]string, 0, len(dbs))
		for db := range dbs {
			names = append(names, db)
		}
		sort.Strings(names)
		var parts []string
		for _, db := range names {
			parts = append(parts, fmt.Sprintf("%s (%d roles)", db, dbs[db]))
		}
		fmt.Fprintf(w, "Postgres roles: %s\n", strings.Join(parts, ", "))
		if len(p.RotatedRoles) > 0 {
			fmt.Fprintf(w, "  rotated this run: %s\n", strings.Join(sortedBools(p.RotatedRoles), ", "))
		}
	}
	if len(p.MissingTF) > 0 {
		fmt.Fprintf(w, "\nTerraform outputs still needed (%d): %s\n", len(p.MissingTF), strings.Join(p.MissingTF, ", "))
		fmt.Fprintf(w, "  → run scripts/prodsecrets.sh export-outputs after terraform apply, then re-run.\n")
	}
	if len(p.MissingSources) > 0 {
		fmt.Fprintf(w, "\nSource secrets not fetched (%d): %s\n", len(p.MissingSources), strings.Join(p.MissingSources, ", "))
		fmt.Fprintf(w, "  → scripts/prodsecrets.sh plan|apply fetches them (Terraform pass 1 creates them).\n")
	}
	if len(p.PendingPrompts) > 0 {
		fmt.Fprintf(w, "\nPrompts still empty (%d):\n", len(p.PendingPrompts))
		for _, pp := range p.PendingPrompts {
			fmt.Fprintf(w, "  - %s\n", pp)
		}
	}
	for _, e := range p.PairErrors {
		fmt.Fprintf(w, "\nKEY PAIR MISMATCH: %s\n", e)
	}
	for _, warn := range p.Warnings {
		fmt.Fprintf(w, "\nWARNING: %s\n", warn)
	}
}

func summarise(c map[Action]int) string {
	order := []Action{ActCreate, ActUpdate, ActReuse, ActKeep, ActPending, ActEmpty}
	var parts []string
	for _, a := range order {
		if c[a] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c[a], a))
		}
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}
