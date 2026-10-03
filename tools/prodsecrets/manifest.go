package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Spec kinds. The manifest header documents each one.
const (
	KindGenerate       = "generate"
	KindConst          = "const"
	KindPrompt         = "prompt"
	KindPromptOptional = "prompt-optional"
	KindPromptFile     = "prompt-file"
	KindTF             = "tf"
	KindDSN            = "dsn"
	KindShared         = "shared"
	KindSharedPrivate  = "shared-private"
	KindSharedPublic   = "shared-public"
	KindCopy           = "copy"
	KindDerive         = "derive"
)

// derivePlaceholderRe matches {tf:<output>} and {copy:<source>#<field>}.
var derivePlaceholderRe = regexp.MustCompile(`\{(tf|copy):([^{}]+)\}`)

// DerivePlaceholders returns the (kind, arg) pairs in a derive: template.
func DerivePlaceholders(tmpl string) [][2]string {
	var out [][2]string
	for _, m := range derivePlaceholderRe.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// Category is the five-way grouping the runbook uses for a spec kind:
// generate, prompt, from-terraform-output, derive, copy (plus literal).
func (s Spec) Category() string {
	switch s.Kind {
	case KindGenerate:
		return "generate"
	case KindPrompt, KindPromptOptional, KindPromptFile:
		return "prompt"
	case KindTF:
		return "terraform-output"
	case KindDSN, KindDerive, KindShared, KindSharedPrivate, KindSharedPublic:
		return "derive"
	case KindCopy:
		return "copy"
	case KindConst:
		return "literal"
	}
	return s.Kind
}

// CopyRef is the parsed argument of copy:<source>#<field>.
type CopyRef struct {
	Source string
	Field  string
}

var fieldRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ParseCopyRef splits "source#field".
func ParseCopyRef(arg string) (CopyRef, error) {
	src, field, ok := strings.Cut(arg, "#")
	if !ok || !keyRe.MatchString(src) || !fieldRe.MatchString(field) {
		return CopyRef{}, fmt.Errorf("copy argument must be <source>#<field>, got %q", arg)
	}
	return CopyRef{Source: src, Field: field}, nil
}

// Source is a secret this tool READS (never writes): one Terraform created
// with generated values. Ref is either a literal Secrets Manager id or
// tf:<output> naming the output that holds the id.
type Source struct {
	Alias string
	Ref   string
	Line  int
}

// TFOutput returns the output name when Ref is tf:<output>.
func (s Source) TFOutput() (string, bool) {
	return strings.CutPrefix(s.Ref, "tf:")
}

// SecretID resolves the Secrets Manager id, using tf for tf:<output> refs.
func (s Source) SecretID(tf map[string]string) (string, bool) {
	if out, ok := s.TFOutput(); ok {
		v := tf[out]
		return v, v != ""
	}
	return s.Ref, true
}

// Generator names (the argument of generate:).
const (
	GenHex32      = "hex32"
	GenHex64      = "hex64"
	GenTOTPHex64  = "totp-hex64"
	GenPassword   = "password"
	GenPIIKeyring = "pii-keyring"
	GenEd25519    = "ed25519-keypair"
	GenRSA2048    = "rsa2048-pem"
)

// Spec is one parsed value specification, "kind:arg".
type Spec struct {
	Kind string
	Arg  string
}

// IsKeypair reports whether the spec generates a two-part key.
func (s Spec) IsKeypair() bool {
	return s.Kind == KindGenerate && (s.Arg == GenEd25519 || s.Arg == GenRSA2048)
}

// IsPrompt reports whether the value comes from the founder.
func (s Spec) IsPrompt() bool {
	return s.Kind == KindPrompt || s.Kind == KindPromptOptional || s.Kind == KindPromptFile
}

func (s Spec) String() string {
	if s.Arg == "" {
		return s.Kind
	}
	return s.Kind + ":" + s.Arg
}

// Entry is a named spec, either a shared value or a key inside a secret.
type Entry struct {
	Name string
	Spec Spec
	Line int
}

// Secret is one Secrets Manager secret: <prefix>/<Name> holding Keys.
type Secret struct {
	Name string
	Keys []Entry
	Line int
}

// Manifest is the parsed manifest.yaml.
type Manifest struct {
	Version int
	Prefix  string
	Sources []Source
	Shared  []Entry
	Secrets []Secret
}

// SourceByAlias finds a source by alias.
func (m *Manifest) SourceByAlias(alias string) (Source, bool) {
	for _, s := range m.Sources {
		if s.Alias == alias {
			return s, true
		}
	}
	return Source{}, false
}

// FullName returns the Secrets Manager name of a secret.
func (m *Manifest) FullName(secret string) string {
	return m.Prefix + "/" + secret
}

// SharedEntry finds a shared entry by name.
func (m *Manifest) SharedEntry(name string) (Entry, bool) {
	for _, e := range m.Shared {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// DSNRef is the parsed argument of dsn:<database>/<role>.
type DSNRef struct {
	Database string
	Role     string
}

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// ParseDSNRef splits "database/role".
func ParseDSNRef(arg string) (DSNRef, error) {
	db, role, ok := strings.Cut(arg, "/")
	if !ok || !identRe.MatchString(db) || !identRe.MatchString(role) {
		return DSNRef{}, fmt.Errorf("dsn argument must be <database>/<role> in lower snake case, got %q", arg)
	}
	return DSNRef{Database: db, Role: role}, nil
}

// ---------------------------------------------------------------------------
// YAML subset parser
//
// Only what the manifest needs: nested mappings of scalar strings, two-space
// indentation, full-line comments, optional single or double quotes around a
// value. Anything else is an error, so a manifest that needs a real YAML
// feature fails loudly instead of being half-read.

type node struct {
	key      string
	value    string
	hasValue bool
	children []*node
	line     int
	indent   int
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func parseYAMLSubset(r io.Reader) (*node, error) {
	root := &node{indent: -1}
	stack := []*node{root}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		if strings.Contains(raw, "\t") {
			return nil, fmt.Errorf("line %d: tabs are not allowed", lineNo)
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent%2 != 0 {
			return nil, fmt.Errorf("line %d: indentation must be a multiple of two spaces", lineNo)
		}
		text := strings.TrimSpace(raw)
		if strings.HasPrefix(text, "- ") || text == "-" {
			return nil, fmt.Errorf("line %d: lists are not supported", lineNo)
		}
		n := &node{line: lineNo, indent: indent}
		if key, rest, ok := strings.Cut(text, ": "); ok {
			n.key = key
			n.value = unquote(strings.TrimSpace(rest))
			n.hasValue = true
		} else if strings.HasSuffix(text, ":") {
			n.key = strings.TrimSuffix(text, ":")
		} else {
			return nil, fmt.Errorf("line %d: expected `key: value` or `key:`", lineNo)
		}
		if !keyRe.MatchString(n.key) {
			return nil, fmt.Errorf("line %d: invalid key %q", lineNo, n.key)
		}
		// Pop to the parent whose indent is smaller than ours.
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		if parent.hasValue {
			return nil, fmt.Errorf("line %d: %q is nested under a scalar", lineNo, n.key)
		}
		if indent != parent.indent+2 && !(parent == root && indent == 0) {
			return nil, fmt.Errorf("line %d: unexpected indentation", lineNo)
		}
		for _, sib := range parent.children {
			if sib.key == n.key {
				return nil, fmt.Errorf("line %d: duplicate key %q (first at line %d)", lineNo, n.key, sib.line)
			}
		}
		parent.children = append(parent.children, n)
		stack = append(stack, n)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return root, nil
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func (n *node) child(key string) *node {
	for _, c := range n.children {
		if c.key == key {
			return c
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Manifest loading and validation

// LoadManifest reads and validates a manifest file.
func LoadManifest(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseManifest(f)
}

// ParseManifest parses and validates a manifest from a reader.
func ParseManifest(r io.Reader) (*Manifest, error) {
	root, err := parseYAMLSubset(r)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	m := &Manifest{}
	for _, c := range root.children {
		switch c.key {
		case "version":
			v, err := strconv.Atoi(c.value)
			if err != nil || v != 1 {
				return nil, fmt.Errorf("manifest line %d: version must be 1", c.line)
			}
			m.Version = v
		case "prefix":
			if !c.hasValue || c.value == "" || strings.HasSuffix(c.value, "/") {
				return nil, fmt.Errorf("manifest line %d: prefix must be a non-empty path without a trailing slash", c.line)
			}
			m.Prefix = c.value
		case "sources":
			for _, s := range c.children {
				if !s.hasValue || strings.TrimSpace(s.value) == "" {
					return nil, fmt.Errorf("manifest line %d: source %q must be a secret id or tf:<output>", s.line, s.key)
				}
				src := Source{Alias: s.key, Ref: s.value, Line: s.line}
				if out, ok := src.TFOutput(); ok && !keyRe.MatchString(out) {
					return nil, fmt.Errorf("manifest line %d: source %q: bad output name %q", s.line, s.key, out)
				}
				m.Sources = append(m.Sources, src)
			}
		case "shared":
			for _, s := range c.children {
				if !s.hasValue {
					return nil, fmt.Errorf("manifest line %d: shared %q must be a scalar spec", s.line, s.key)
				}
				spec, err := parseSpec(s.value)
				if err != nil {
					return nil, fmt.Errorf("manifest line %d: shared %q: %w", s.line, s.key, err)
				}
				m.Shared = append(m.Shared, Entry{Name: s.key, Spec: spec, Line: s.line})
			}
		case "secrets":
			for _, s := range c.children {
				if s.hasValue {
					return nil, fmt.Errorf("manifest line %d: secret %q must be a mapping of keys", s.line, s.key)
				}
				sec := Secret{Name: s.key, Line: s.line}
				for _, k := range s.children {
					if !k.hasValue {
						return nil, fmt.Errorf("manifest line %d: key %q must be a scalar spec", k.line, k.key)
					}
					spec, err := parseSpec(k.value)
					if err != nil {
						return nil, fmt.Errorf("manifest line %d: %s.%s: %w", k.line, s.key, k.key, err)
					}
					sec.Keys = append(sec.Keys, Entry{Name: k.key, Spec: spec, Line: k.line})
				}
				m.Secrets = append(m.Secrets, sec)
			}
		default:
			return nil, fmt.Errorf("manifest line %d: unknown top-level key %q", c.line, c.key)
		}
	}
	if m.Version == 0 {
		return nil, fmt.Errorf("manifest: version is required")
	}
	if m.Prefix == "" {
		return nil, fmt.Errorf("manifest: prefix is required")
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func parseSpec(s string) (Spec, error) {
	kind, arg, _ := strings.Cut(s, ":")
	kind = strings.TrimSpace(kind)
	spec := Spec{Kind: kind, Arg: arg}
	switch kind {
	case KindGenerate:
		switch arg {
		case GenHex32, GenHex64, GenTOTPHex64, GenPassword, GenPIIKeyring, GenEd25519, GenRSA2048:
		default:
			return spec, fmt.Errorf("unknown generator %q", arg)
		}
	case KindConst:
		// Anything goes, including empty.
	case KindPrompt, KindPromptOptional, KindPromptFile:
		if strings.TrimSpace(arg) == "" {
			return spec, fmt.Errorf("%s needs a label", kind)
		}
	case KindTF, KindShared, KindSharedPrivate, KindSharedPublic:
		if !keyRe.MatchString(arg) {
			return spec, fmt.Errorf("%s needs a name, got %q", kind, arg)
		}
	case KindDSN:
		if _, err := ParseDSNRef(arg); err != nil {
			return spec, err
		}
	case KindCopy:
		if _, err := ParseCopyRef(arg); err != nil {
			return spec, err
		}
	case KindDerive:
		ph := DerivePlaceholders(arg)
		if len(ph) == 0 {
			return spec, fmt.Errorf("derive needs at least one {tf:<output>} or {copy:<source>#<field>}, got %q", arg)
		}
		if strings.Count(arg, "{") != len(ph) || strings.Count(arg, "}") != len(ph) {
			return spec, fmt.Errorf("derive template %q has an unbalanced or unknown placeholder", arg)
		}
		for _, p := range ph {
			switch p[0] {
			case "tf":
				if !keyRe.MatchString(p[1]) {
					return spec, fmt.Errorf("derive: bad output name %q", p[1])
				}
			case "copy":
				if _, err := ParseCopyRef(p[1]); err != nil {
					return spec, err
				}
			}
		}
	default:
		return spec, fmt.Errorf("unknown spec kind %q", kind)
	}
	return spec, nil
}

func (m *Manifest) validate() error {
	sources := map[string]bool{}
	for _, s := range m.Sources {
		if sources[s.Alias] {
			return fmt.Errorf("manifest line %d: duplicate source %q", s.Line, s.Alias)
		}
		sources[s.Alias] = true
	}
	checkCopy := func(line int, what string, spec Spec) error {
		var refs []string
		switch spec.Kind {
		case KindCopy:
			refs = []string{spec.Arg}
		case KindDerive:
			for _, p := range DerivePlaceholders(spec.Arg) {
				if p[0] == "copy" {
					refs = append(refs, p[1])
				}
			}
		}
		for _, arg := range refs {
			ref, _ := ParseCopyRef(arg)
			if !sources[ref.Source] {
				return fmt.Errorf("manifest line %d: %s copies from unknown source %q", line, what, ref.Source)
			}
		}
		return nil
	}
	shared := map[string]Entry{}
	for _, e := range m.Shared {
		switch e.Spec.Kind {
		case KindGenerate, KindConst, KindPrompt, KindPromptOptional, KindPromptFile, KindTF, KindCopy:
		default:
			return fmt.Errorf("manifest line %d: shared %q cannot be %s", e.Line, e.Name, e.Spec.Kind)
		}
		if err := checkCopy(e.Line, "shared "+e.Name, e.Spec); err != nil {
			return err
		}
		shared[e.Name] = e
	}
	seenSecrets := map[string]bool{}
	privateHolders := map[string]int{}
	for _, s := range m.Secrets {
		for _, k := range s.Keys {
			if k.Spec.Kind == KindSharedPrivate {
				privateHolders[k.Spec.Arg]++
			}
		}
	}
	for _, e := range m.Shared {
		if e.Spec.IsKeypair() && privateHolders[e.Name] == 0 {
			// The private half would be generated and thrown away: every
			// receiver would trust a key nobody can sign with.
			return fmt.Errorf("manifest line %d: keypair %q has no shared-private holder", e.Line, e.Name)
		}
	}
	for _, s := range m.Secrets {
		if seenSecrets[s.Name] {
			return fmt.Errorf("manifest line %d: duplicate secret %q", s.Line, s.Name)
		}
		seenSecrets[s.Name] = true
		for _, k := range s.Keys {
			if err := checkCopy(k.Line, s.Name+"."+k.Name, k.Spec); err != nil {
				return err
			}
			switch k.Spec.Kind {
			case KindGenerate:
				if k.Spec.IsKeypair() {
					return fmt.Errorf("manifest line %d: %s.%s: keypairs must be shared so both halves can be referenced", k.Line, s.Name, k.Name)
				}
			case KindShared:
				ref, ok := shared[k.Spec.Arg]
				if !ok {
					return fmt.Errorf("manifest line %d: %s.%s refers to unknown shared %q", k.Line, s.Name, k.Name, k.Spec.Arg)
				}
				if ref.Spec.IsKeypair() {
					return fmt.Errorf("manifest line %d: %s.%s: %q is a keypair; use shared-private or shared-public", k.Line, s.Name, k.Name, k.Spec.Arg)
				}
			case KindSharedPrivate, KindSharedPublic:
				ref, ok := shared[k.Spec.Arg]
				if !ok {
					return fmt.Errorf("manifest line %d: %s.%s refers to unknown shared %q", k.Line, s.Name, k.Name, k.Spec.Arg)
				}
				if !ref.Spec.IsKeypair() {
					return fmt.Errorf("manifest line %d: %s.%s: %q is not a keypair", k.Line, s.Name, k.Name, k.Spec.Arg)
				}
			}
		}
	}
	return nil
}

// SecretNames returns the short names in manifest order.
func (m *Manifest) SecretNames() []string {
	out := make([]string, 0, len(m.Secrets))
	for _, s := range m.Secrets {
		out = append(out, s.Name)
	}
	return out
}

// Summary counts keys by category (generate, prompt, terraform-output,
// derive, copy, literal) across all secrets, for --plan.
func (m *Manifest) Summary() (secrets, keys int, byCategory map[string]int) {
	byCategory = map[string]int{}
	for _, s := range m.Secrets {
		secrets++
		for _, k := range s.Keys {
			keys++
			cat := k.Spec.Category()
			switch k.Spec.Kind {
			case KindShared, KindSharedPrivate, KindSharedPublic:
				// Count a shared reference where its value comes from.
				if e, ok := m.SharedEntry(k.Spec.Arg); ok {
					cat = e.Spec.Category()
				}
			}
			byCategory[cat]++
		}
	}
	return
}

// sortedKeys is a small helper used by several writers.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
