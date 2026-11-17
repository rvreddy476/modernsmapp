// Command kafka-topics checks that every Kafka topic the Go services name is
// in the MSK topic list (infra/terraform/modules/msk/topics-job/variables.tf).
// MSK runs with auto.create.topics.enable=false, so a topic missing from that
// list does not exist in QA or prod, and a producer to it fails (reels-3:
// media.events.long was missing and every long video fell back to the reel
// consumer).
//
//	go run scripts/ci/kafka-topics/main.go [-root .] [-v]
//
// Stdlib only, no go.mod: it parses the code with go/parser and never builds
// it. Topic names are taken from:
//   - a const, var or assignment whose name contains "topic"
//     (intakeTopic = "media.events", longTopic = intakeTopic + ".long");
//   - a struct field or selector whose name contains "topic"
//     (kafka.ReaderConfig{Topic: "platform-events"}, cfg.DLQTopic = "...");
//   - the default after an env name containing TOPIC in a call
//     (getenv("KAFKA_TOPIC", "dating-events"));
//   - KEY_..._TOPIC: value lines of deploy/services/*/values*.yaml.
//
// Identifiers are resolved within one package directory, through + chains.
// Only Kafka-shaped values count: lower case, with a '.' or '-' separator.
// Names that are not Kafka topics go in notKafka below, with the reason.
// Skipped: *_test.go, vendor/, testdata/, node_modules/.
//
// Prints one line per missing topic, then PASS/FAIL and RESULT, like
// scripts/verify. Exit 0 = pass, 1 = topic missing, 2 = could not check.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const terraformTopics = "infra/terraform/modules/msk/topics-job/variables.tf"

// Workspaces whose code is scanned.
var codeRoots = []string{"Architecture", "identity-platform", "chat-service"}

// Values that look like topics but are not Kafka topics.
var notKafka = map[string]string{
	"doorstep.admin.live": "doorstep-service SSE realtime channel (internal/service/realtime.go), not Kafka",
}

// Topics the code names that are knowingly not provisioned yet. Each is
// printed as KNOWN-MISSING on every run and does not fail the check; the
// entry must go when the topic is added to the list (the check fails on a
// stale entry). Never add one here to make a new topic pass.
var knownMissing = map[string]string{}

var (
	topicName = regexp.MustCompile(`(?i)topic`)
	envName   = regexp.MustCompile(`^[A-Z0-9_]*TOPIC[A-Z0-9_]*$`)
	// Lower case with at least one '.' or '-' separator: "topic_create"
	// (an audit action) and "related_topic" (a feed source) are not topics.
	kafkaShape = regexp.MustCompile(`^[a-z][a-z0-9_]*([.-][a-z0-9_]+)+$`)
	valuesLine = regexp.MustCompile(`^\s*[A-Z0-9_]*TOPIC[A-Z0-9_]*:\s*["']?([^"'\s#]+)`)
	tfString   = regexp.MustCompile(`^\s*"([^"]+)"\s*,?\s*(#.*)?$`)
	tfOverride = regexp.MustCompile(`^\s*topics\s*=`)
)

type pkg struct {
	defs map[string][]ast.Expr
}

// found maps a topic to the places that name it.
type found map[string][]string

func (f found) add(topic, where string) {
	if !kafkaShape.MatchString(topic) {
		return
	}
	if _, skip := notKafka[topic]; skip {
		return
	}
	f[topic] = append(f[topic], where)
}

func main() {
	root := flag.String("root", ".", "repository root")
	verbose := flag.Bool("v", false, "list every topic found and where")
	flag.Parse()

	listed, err := readTerraformList(filepath.Join(*root, terraformTopics))
	if err != nil {
		fail(err)
	}
	if err := checkNoEnvOverride(*root); err != nil {
		fail(err)
	}

	used := found{}
	for _, r := range codeRoots {
		if err := scanGo(*root, r, used); err != nil {
			fail(err)
		}
	}
	if err := scanValues(*root, used); err != nil {
		fail(err)
	}
	if len(used) == 0 {
		fail(fmt.Errorf("found no topic in the code: the extraction is broken"))
	}

	names := make([]string, 0, len(used))
	for t := range used {
		names = append(names, t)
	}
	sort.Strings(names)

	var missing, stale []string
	for t := range knownMissing {
		if listed[t] {
			stale = append(stale, t)
		}
	}
	sort.Strings(stale)
	for _, t := range stale {
		fmt.Printf("STALE   %-30s is listed now: delete its knownMissing entry\n", t)
	}
	for _, t := range names {
		if *verbose {
			fmt.Printf("topic %-32s %s\n", t, strings.Join(first(used[t], 3), ", "))
		}
		if why, known := knownMissing[t]; known && !listed[t] {
			fmt.Printf("KNOWN-MISSING %-24s named at %s: %s\n", t, strings.Join(first(used[t], 3), ", "), why)
			continue
		}
		if !listed[t] {
			missing = append(missing, t)
			fmt.Printf("MISSING %-30s named at %s\n", t, strings.Join(first(used[t], 3), ", "))
		}
	}
	var unused []string
	for t := range listed {
		if _, ok := used[t]; !ok {
			unused = append(unused, t)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		fmt.Printf("note: listed but not found in code (fine, kept): %s\n", strings.Join(unused, ", "))
	}

	if len(missing) > 0 || len(stale) > 0 {
		fmt.Printf("FAIL kafka-topics   %d of %d topics named in code are not in %s; %d stale knownMissing entries\n",
			len(missing), len(names), terraformTopics, len(stale))
		fmt.Println("RESULT FAIL (1 failed)")
		os.Exit(1)
	}
	fmt.Printf("PASS kafka-topics   all %d topics named in code are in %s\n", len(names), terraformTopics)
	fmt.Println("RESULT PASS")
}

func fail(err error) {
	fmt.Printf("FAIL kafka-topics   %v\n", err)
	fmt.Println("RESULT FAIL (1 failed)")
	os.Exit(2)
}

func first(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), fmt.Sprintf("+%d more", len(s)-n))
}

// readTerraformList returns the default of variable "topics".
func readTerraformList(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	state := 0 // 0 before the variable, 1 inside it, 2 inside default = [
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch state {
		case 0:
			if strings.HasPrefix(strings.TrimSpace(line), `variable "topics"`) {
				state = 1
			}
		case 1:
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "default") && strings.HasSuffix(t, "[") {
				state = 2
			}
		case 2:
			if strings.TrimSpace(line) == "]" {
				if len(out) == 0 {
					return nil, fmt.Errorf("%s: variable \"topics\" default is empty", path)
				}
				return out, nil
			}
			if m := tfString.FindStringSubmatch(line); m != nil {
				out[m[1]] = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%s: no variable \"topics\" with a default = [ ... ] list", path)
}

// checkNoEnvOverride refuses an env that passes its own topics list: this
// check reads only the module default.
func checkNoEnvOverride(root string) error {
	envs, _ := filepath.Glob(filepath.Join(root, "infra/terraform/envs/*/main.tf"))
	for _, p := range envs {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		in := false
		for i, line := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, `module "msk_topics"`) {
				in = true
				continue
			}
			if in && strings.HasPrefix(line, "}") {
				in = false
			}
			if in && tfOverride.MatchString(line) {
				return fmt.Errorf("%s:%d passes its own topics list; extend this check to read it", p, i+1)
			}
		}
	}
	return nil
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".")
}

func scanGo(root, sub string, used found) error {
	base := filepath.Join(root, sub)
	if _, err := os.Stat(base); err != nil {
		return fmt.Errorf("%s: %w", sub, err)
	}
	byDir := map[string][]string{}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != base && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			byDir[filepath.Dir(p)] = append(byDir[filepath.Dir(p)], p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, files := range byDir {
		if err := scanPackage(root, files, used); err != nil {
			return err
		}
	}
	return nil
}

func scanPackage(root string, files []string, used found) error {
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, p := range files {
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		parsed = append(parsed, f)
	}
	pk := &pkg{defs: map[string][]ast.Expr{}}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for i, id := range n.Names {
					if i < len(n.Values) {
						pk.defs[id.Name] = append(pk.defs[id.Name], n.Values[i])
					}
				}
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					for i, l := range n.Lhs {
						if id, ok := l.(*ast.Ident); ok {
							pk.defs[id.Name] = append(pk.defs[id.Name], n.Rhs[i])
						}
					}
				}
			}
			return true
		})
	}
	rel := func(pos token.Pos) string {
		p := fset.Position(pos)
		r, err := filepath.Rel(root, p.Filename)
		if err != nil {
			r = p.Filename
		}
		return fmt.Sprintf("%s:%d", filepath.ToSlash(r), p.Line)
	}
	addAll := func(e ast.Expr) {
		for _, v := range pk.resolve(e, 0) {
			used.add(v, rel(e.Pos()))
		}
	}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for i, id := range n.Names {
					if i < len(n.Values) && topicName.MatchString(id.Name) {
						addAll(n.Values[i])
					}
				}
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					for i, l := range n.Lhs {
						if topicName.MatchString(lhsName(l)) {
							addAll(n.Rhs[i])
						}
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && topicName.MatchString(id.Name) {
					addAll(n.Value)
				}
			case *ast.CallExpr:
				for i := 0; i+1 < len(n.Args); i++ {
					if s, ok := literal(n.Args[i]); ok && envName.MatchString(s) {
						addAll(n.Args[i+1])
					}
				}
			}
			return true
		})
	}
	return nil
}

func lhsName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

func literal(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	return s, err == nil
}

// resolve returns the string values e can have, following identifiers
// defined in the same package and + concatenation.
func (p *pkg) resolve(e ast.Expr, depth int) []string {
	if depth > 8 {
		return nil
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if s, ok := literal(e); ok {
			return []string{s}
		}
	case *ast.ParenExpr:
		return p.resolve(e.X, depth+1)
	case *ast.Ident:
		var out []string
		for _, d := range p.defs[e.Name] {
			out = append(out, p.resolve(d, depth+1)...)
		}
		return out
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return nil
		}
		var out []string
		for _, x := range p.resolve(e.X, depth+1) {
			for _, y := range p.resolve(e.Y, depth+1) {
				out = append(out, x+y)
			}
		}
		return out
	}
	return nil
}

func scanValues(root string, used found) error {
	files, _ := filepath.Glob(filepath.Join(root, "deploy/services/*/values*.yaml"))
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		n := 0
		for sc.Scan() {
			n++
			if m := valuesLine.FindStringSubmatch(sc.Text()); m != nil {
				r, _ := filepath.Rel(root, p)
				used.add(m[1], fmt.Sprintf("%s:%d", filepath.ToSlash(r), n))
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	return nil
}
