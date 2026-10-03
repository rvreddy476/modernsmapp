// prodsecrets fills the per-service production secrets in AWS Secrets Manager
// from tools/prodsecrets/manifest.yaml. It never talks to AWS itself and has
// no AWS SDK: it EMITS one JSON payload per secret (mode 0600, outside the
// repository by default) plus the exact `aws secretsmanager put-secret-value
// --secret-string file://...` script, and scripts/prodsecrets.sh runs that
// with the lead's own credentials.
//
//	scripts/prodsecrets.sh plan                               # what would change, no values
//	scripts/prodsecrets.sh apply                              # generate, prompt, write payloads
//	scripts/prodsecrets.sh apply --rotate shared/svc_commerce # re-key one thing
//
// Inputs, all under --out (default ~/.atpost/prodsecrets/prod):
//
//	tf-outputs.json   `terraform output -json` (no secrets in it)
//	current/*.json    what Secrets Manager holds now, one file per manifest secret
//	sources/*.json    the Terraform-generated secrets copy: keys read from
//	secrets/*.json    payloads an earlier apply wrote and nobody pushed yet
//
// The never-overwrite rule: a key that already has a value in Secrets
// Manager keeps it unless named by --rotate. That is why --apply refuses to
// run without current/ — it must know what is there. Values are never
// printed; the report shows key names, actions and origins only. state.json
// records key ids and timestamps, never values.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// newPrompter is replaced by tests so a run can be driven without a terminal.
var newPrompter = func(stderr io.Writer) Prompter { return TerminalPrompter{Out: stderr} }

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// DefaultOutDir is outside any checkout so an emitted payload can never be
// committed by accident.
func DefaultOutDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "atpost-prodsecrets", "prod")
	}
	return filepath.Join(home, ".atpost", "prodsecrets", "prod")
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("prodsecrets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		manifestPath = fs.String("manifest", defaultManifestPath(), "manifest file")
		outDir       = fs.String("out", DefaultOutDir(), "working directory for inputs and emitted payloads (must be outside the repository)")
		tfPath       = fs.String("tf", "", "Terraform outputs JSON (default: <out>/tf-outputs.json)")
		region       = fs.String("region", "ap-south-1", "AWS region written into the push script")
		plan         = fs.Bool("plan", false, "show what would be created or updated; write nothing")
		apply        = fs.Bool("apply", false, "generate, prompt and write the payloads")
		list         = fs.Bool("list", false, "print `<short> <secret id>` for every secret to fill")
		listSources  = fs.Bool("list-sources", false, "print `<alias> <secret id>` for every secret to copy from")
		verbose      = fs.Bool("verbose", false, "also list keys that are kept unchanged")
		rotate       stringList
	)
	fs.Var(&rotate, "rotate", "re-key or re-copy: shared/<name>, <secret>/<key>, role/<role>, source/<alias> (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if err := checkOutDir(*outDir); err != nil {
		return err
	}
	if *tfPath == "" {
		*tfPath = filepath.Join(*outDir, "tf-outputs.json")
	}

	m, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	if *list {
		for _, s := range m.Secrets {
			fmt.Fprintf(stdout, "%s %s\n", s.Name, m.FullName(s.Name))
		}
		return nil
	}
	tf, err := LoadTFOutputs(*tfPath)
	if err != nil {
		return err
	}
	if *listSources {
		for _, s := range m.Sources {
			id, ok := s.SecretID(tf)
			if !ok {
				out, _ := s.TFOutput()
				fmt.Fprintf(stderr, "source %s: terraform output %s missing from %s\n", s.Alias, out, *tfPath)
				continue
			}
			fmt.Fprintf(stdout, "%s %s\n", s.Alias, id)
		}
		return nil
	}
	if *plan == *apply {
		return errors.New("choose exactly one of --plan or --apply")
	}

	statePath := filepath.Join(*outDir, "state.json")
	st, err := LoadState(statePath)
	if err != nil {
		return err
	}
	current, currentFetched, err := LoadValueDir(filepath.Join(*outDir, "current"))
	if err != nil {
		return err
	}
	pending, _, err := LoadValueDir(filepath.Join(*outDir, "secrets"))
	if err != nil {
		return err
	}
	sources, _, err := LoadValueDir(filepath.Join(*outDir, "sources"))
	if err != nil {
		return err
	}
	if *apply {
		// Never overwrite what is there: apply must see every secret's
		// current contents (an empty shell is fetched as {}).
		var missing []string
		for _, s := range m.Secrets {
			if _, ok := current[s.Name]; !ok {
				missing = append(missing, s.Name)
			}
		}
		if !currentFetched || len(missing) > 0 {
			return fmt.Errorf("--apply needs what Secrets Manager holds now (%d secret(s) not fetched into %s); run it through scripts/prodsecrets.sh apply, which fetches first",
				len(missing), filepath.Join(*outDir, "current"))
		}
	}

	rot := map[string]bool{}
	for _, r := range rotate {
		rot[r] = true
	}
	in := Inputs{Current: current, Unversioned: UnversionedIn(filepath.Join(*outDir, "current")), Pending: pending, Sources: sources, TF: tf, Rotate: rot, Apply: *apply}
	if *apply {
		in.Prompter = newPrompter(stderr)
	}

	p, resolveErr := Resolve(m, in)
	if p == nil {
		return resolveErr
	}
	red := NewRedactor(p, in)
	out := red.Writer(stdout)
	PrintPlan(out, m, p, *apply, *verbose)
	if resolveErr != nil {
		return errors.New(red.Redact(resolveErr.Error()))
	}
	if !*apply {
		if !currentFetched {
			fmt.Fprintf(out, "\nNOTE: %s is absent, so this plan assumes every secret is empty.\n", filepath.Join(*outDir, "current"))
		}
		fmt.Fprintf(out, "\nNext: scripts/prodsecrets.sh apply\n")
		return nil
	}
	if len(p.MissingTF) > 0 || len(p.MissingSources) > 0 {
		return fmt.Errorf("refusing to emit payloads with Terraform values missing (%d output(s), %d source(s)); finish terraform pass 1, then scripts/prodsecrets.sh export-outputs",
			len(p.MissingTF), len(p.MissingSources))
	}

	now := time.Now().UTC()
	written, err := WriteOutputs(*outDir, m, p, *region, now)
	if err != nil {
		return err
	}
	st.Record(p, sortedBools(rot), now)
	if err := SaveState(statePath, st); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nWritten (mode 0600, outside the repository):\n")
	fmt.Fprintf(out, "  %d payload(s) under %s\n", len(written.SecretFiles), filepath.Join(*outDir, "secrets"))
	if len(written.Removed) > 0 {
		fmt.Fprintf(out, "  removed %d stale payload(s) for secrets that need no push\n", len(written.Removed))
	}
	fmt.Fprintf(out, "  %s  (key ids and timestamps only)\n", statePath)
	fmt.Fprintf(out, "  %s\n", written.PushScript)
	if written.RolesSQL != "" {
		fmt.Fprintf(out, "  %s  (CONTAINS PASSWORDS)\n", written.RolesSQL)
	}
	for _, f := range written.PublicFiles {
		fmt.Fprintf(out, "  %s  (public half, safe to share)\n", f)
	}
	fmt.Fprintf(out, "\nChecklist:\n")
	step := 1
	if written.RolesSQL != "" {
		fmt.Fprintf(out, "  %d. scripts/prodsecrets.sh roles     (CREATE ROLE / GRANT on Aurora, from inside the cluster; deletes roles.sql)\n", step)
		step++
	}
	if len(p.PendingPrompts) > 0 {
		fmt.Fprintf(out, "  %d. %d prompt(s) still empty (listed above): re-run apply when the values exist; nothing else changes.\n", step, len(p.PendingPrompts))
		step++
	}
	fmt.Fprintf(out, "  %d. scripts/prodsecrets.sh push      (pushes %d secret(s), deleting each payload after it lands)\n", step, countChanged(p))
	step++
	fmt.Fprintf(out, "  %d. scripts/prodsecrets.sh plan      (expect every key: keep)\n", step)
	return nil
}

func countChanged(p *Plan) int {
	n := 0
	for _, s := range p.Secrets {
		if s.Changed() {
			n++
		}
	}
	return n
}

func defaultManifestPath() string {
	for _, c := range []string{"tools/prodsecrets/manifest.yaml", "manifest.yaml"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "tools/prodsecrets/manifest.yaml"
}

// checkOutDir refuses an output directory inside a git checkout (other than
// the gitignored tools/prodsecrets/out), so payloads cannot be committed.
func checkOutDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for d := abs; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			rel, _ := filepath.Rel(d, abs)
			rel = filepath.ToSlash(rel)
			if rel == "tools/prodsecrets/out" || strings.HasPrefix(rel, "tools/prodsecrets/out/") {
				return nil
			}
			return fmt.Errorf("--out %s is inside the git checkout %s; use a directory outside it (default %s)", abs, d, DefaultOutDir())
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil
		}
		d = parent
	}
}
