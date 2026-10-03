package main

import (
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// Action is what the run does to one key.
type Action string

const (
	ActCreate  Action = "create"  // Secrets Manager has no value; this run supplies one
	ActUpdate  Action = "update"  // Secrets Manager's value is replaced (only via --rotate)
	ActKeep    Action = "keep"    // the value Secrets Manager already holds is kept
	ActReuse   Action = "reuse"   // same value as the unpushed payload of an earlier apply
	ActPending Action = "pending" // the source is not available yet; written as ""
	ActEmpty   Action = "empty"   // deliberately empty (const: or an optional prompt left blank)
)

// Origin says where a value came from. It is safe to print.
type Origin string

const (
	OriginCurrent   Origin = "current"   // the value already in Secrets Manager
	OriginPending   Origin = "unpushed"  // an earlier apply's payload not yet pushed
	OriginGenerated Origin = "generated" // freshly generated
	OriginPrompt    Origin = "prompt"    // typed at a prompt
	OriginTF        Origin = "terraform" // a Terraform output
	OriginCopy      Origin = "copy"      // read from a Terraform-generated secret
	OriginDerived   Origin = "derived"   // assembled from other values (DSN, shared refs)
	OriginConst     Origin = "const"
	OriginNone      Origin = "-"
)

// KeyResult is the plan for one key. Value is never printed by the tool.
type KeyResult struct {
	Secret string
	Key    string
	Spec   Spec
	Action Action
	Origin Origin
	Reason string // why pending / empty
	Value  string
}

// SecretPlan is the plan for one secret.
type SecretPlan struct {
	Name     string
	FullName string
	Keys     []KeyResult
	// FirstVersion: Secrets Manager holds no version yet (an empty shell
	// straight from Terraform), so the payload is pushed even when every key
	// is empty: External Secrets cannot sync a secret without a version.
	FirstVersion bool
}

// Changed reports whether the secret must be pushed: any key created,
// updated or reused (a reused value is still unpushed).
func (s SecretPlan) Changed() bool {
	if s.FirstVersion {
		return true
	}
	for _, k := range s.Keys {
		if k.Action == ActCreate || k.Action == ActUpdate || k.Action == ActReuse {
			return true
		}
	}
	return false
}

// Payload is the JSON object to push: every key, pending ones as "".
// put-secret-value replaces the whole object, so kept values are included.
func (s SecretPlan) Payload() map[string]string {
	out := make(map[string]string, len(s.Keys))
	for _, k := range s.Keys {
		out[k.Key] = k.Value
	}
	return out
}

// RoleGrant records that a role needs access to a database.
type RoleGrant struct {
	Role     string
	Database string
}

// Plan is the result of a resolution run.
type Plan struct {
	Secrets        []SecretPlan
	MissingTF      []string // Terraform output names not present
	MissingSources []string // source aliases (or alias#field) not fetched
	PendingPrompts []string // "secret.key — label"
	Warnings       []string
	PairErrors     []string           // caller/receiver halves that do not match
	Roles          []RoleGrant        // every (role, database) pair the manifest uses
	RolePasswords  map[string]string  // role -> password (never printed)
	RotatedRoles   map[string]bool    // roles whose password changed this run
	PublicHalves   map[string]Keypair // shared keypairs (public halves are safe to write out)
}

// Counts returns the action tally for the checklist.
func (p *Plan) Counts() map[Action]int {
	c := map[Action]int{}
	for _, s := range p.Secrets {
		for _, k := range s.Keys {
			c[k.Action]++
		}
	}
	return c
}

// Prompter asks the founder for a value. Implementations must not echo.
type Prompter interface {
	Ask(label string) (string, error)
}

// Inputs is everything a resolution run needs besides the manifest. No input
// comes from local state: values live only in Secrets Manager (Current),
// in unpushed payloads (Pending) and in the Terraform-generated secrets
// (Sources).
type Inputs struct {
	Current     map[string]map[string]string // secret short name -> key -> value (Secrets Manager now)
	Unversioned map[string]bool              // secrets fetched with no version at all (empty shells)
	Pending     map[string]map[string]string // secret short name -> key -> value (out/secrets, not pushed)
	Sources     map[string]map[string]string // source alias -> field -> value
	TF          map[string]string
	Rotate      map[string]bool // shared/<name>, <secret>/<key>, role/<role>, source/<alias>
	Prompter    Prompter        // nil in plan mode: prompts become pending
	Gen         Generator
	Apply       bool
	// ReadFile lets tests stub prompt-file reads.
	ReadFile func(path string) ([]byte, error)
}

type resolver struct {
	m      *Manifest
	in     Inputs
	plan   *Plan
	shared map[string]*sharedValue // memo
}

type sharedValue struct {
	entry   Entry
	scalar  string
	pair    Keypair
	origin  Origin
	pending string // reason when the value is not available
}

// Resolve computes the plan. It may prompt in apply mode; it never writes
// anything. In apply mode a caller/receiver key mismatch is an error.
func Resolve(m *Manifest, in Inputs) (*Plan, error) {
	if in.Rotate == nil {
		in.Rotate = map[string]bool{}
	}
	if in.ReadFile == nil {
		in.ReadFile = os.ReadFile
	}
	r := &resolver{
		m:      m,
		in:     in,
		plan:   &Plan{RolePasswords: map[string]string{}, RotatedRoles: map[string]bool{}, PublicHalves: map[string]Keypair{}},
		shared: map[string]*sharedValue{},
	}
	for _, target := range sortedBools(in.Rotate) {
		if err := r.checkRotateTarget(target); err != nil {
			return nil, err
		}
	}
	for _, s := range m.Secrets {
		_, fetched := in.Current[s.Name]
		sp := SecretPlan{Name: s.Name, FullName: m.FullName(s.Name), FirstVersion: !fetched || in.Unversioned[s.Name]}
		for _, k := range s.Keys {
			kr, err := r.resolveKey(s.Name, k)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", s.Name, k.Name, err)
			}
			sp.Keys = append(sp.Keys, kr)
		}
		r.plan.Secrets = append(r.plan.Secrets, sp)
	}
	r.verifyPairs()
	r.collectRoles()
	r.adoptKeptRolePasswords()
	sort.Strings(r.plan.MissingTF)
	r.plan.MissingTF = dedupe(r.plan.MissingTF)
	sort.Strings(r.plan.MissingSources)
	r.plan.MissingSources = dedupe(r.plan.MissingSources)
	if in.Apply && len(r.plan.PairErrors) > 0 {
		return r.plan, fmt.Errorf("refusing to emit mismatched key pairs:\n  %s", strings.Join(r.plan.PairErrors, "\n  "))
	}
	return r.plan, nil
}

func (r *resolver) checkRotateTarget(t string) error {
	kind, name, ok := strings.Cut(t, "/")
	if !ok {
		return fmt.Errorf("--rotate %q: use shared/<name>, <secret>/<key>, role/<role> or source/<alias>", t)
	}
	switch kind {
	case "shared":
		e, ok := r.m.SharedEntry(name)
		if !ok {
			return fmt.Errorf("--rotate %q: no shared value %q", t, name)
		}
		switch e.Spec.Kind {
		case KindConst:
			return fmt.Errorf("--rotate %q: a const cannot be rotated; edit the manifest", t)
		case KindCopy:
			ref, _ := ParseCopyRef(e.Spec.Arg)
			return fmt.Errorf("--rotate %q: this value is copied from %s; rotate it there, then --rotate source/%s", t, ref.Source, ref.Source)
		}
		return nil
	case "source":
		if _, ok := r.m.SourceByAlias(name); !ok {
			return fmt.Errorf("--rotate %q: no source %q", t, name)
		}
		return nil
	case "role":
		for _, s := range r.m.Secrets {
			for _, k := range s.Keys {
				if k.Spec.Kind == KindDSN {
					ref, _ := ParseDSNRef(k.Spec.Arg)
					if ref.Role == name {
						return nil
					}
				}
			}
		}
		return fmt.Errorf("--rotate %q: no dsn: key uses role %q", t, name)
	default:
		for _, s := range r.m.Secrets {
			if s.Name != kind {
				continue
			}
			for _, k := range s.Keys {
				if k.Name != name {
					continue
				}
				switch k.Spec.Kind {
				case KindShared, KindSharedPrivate, KindSharedPublic:
					return fmt.Errorf("--rotate %q: this key copies shared %q; rotate shared/%s instead", t, k.Spec.Arg, k.Spec.Arg)
				case KindDSN:
					ref, _ := ParseDSNRef(k.Spec.Arg)
					return fmt.Errorf("--rotate %q: this key is a DSN; rotate role/%s instead", t, ref.Role)
				}
				// generate/prompt: a new value. const/tf/copy: re-read the
				// manifest literal, the output or the source.
				return nil
			}
			return fmt.Errorf("--rotate %q: secret %q has no key %q", t, kind, name)
		}
		return fmt.Errorf("--rotate %q: no secret %q", t, kind)
	}
}

func lookup(m map[string]map[string]string, secret, key string) (string, bool) {
	if cur, ok := m[secret]; ok {
		if v, ok := cur[key]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}

// currentValue is the value already in Secrets Manager, if any.
func (r *resolver) currentValue(secret, key string) (string, bool) {
	return lookup(r.in.Current, secret, key)
}

// pendingValue is the value an earlier apply wrote but nobody pushed yet.
func (r *resolver) pendingValue(secret, key string) (string, bool) {
	return lookup(r.in.Pending, secret, key)
}

// rotating reports whether a key must be replaced even if a value exists.
func (r *resolver) rotating(secret string, k Entry) bool {
	if r.in.Rotate[secret+"/"+k.Name] {
		return true
	}
	switch k.Spec.Kind {
	case KindShared, KindSharedPrivate, KindSharedPublic:
		if r.in.Rotate["shared/"+k.Spec.Arg] {
			return true
		}
		if e, ok := r.m.SharedEntry(k.Spec.Arg); ok && e.Spec.Kind == KindCopy {
			ref, _ := ParseCopyRef(e.Spec.Arg)
			return r.in.Rotate["source/"+ref.Source]
		}
	case KindDSN:
		ref, _ := ParseDSNRef(k.Spec.Arg)
		return r.in.Rotate["role/"+ref.Role]
	case KindCopy:
		ref, _ := ParseCopyRef(k.Spec.Arg)
		return r.in.Rotate["source/"+ref.Source]
	case KindDerive:
		for _, p := range DerivePlaceholders(k.Spec.Arg) {
			if p[0] == "copy" {
				ref, _ := ParseCopyRef(p[1])
				if r.in.Rotate["source/"+ref.Source] {
					return true
				}
			}
		}
	}
	return false
}

// deriveValue fills a derive: template from Terraform outputs and sources.
func (r *resolver) deriveValue(tmpl string) (string, Origin, string) {
	var missing string
	out := derivePlaceholderRe.ReplaceAllStringFunc(tmpl, func(ph string) string {
		m := derivePlaceholderRe.FindStringSubmatch(ph)
		var v, reason string
		switch m[1] {
		case "tf":
			v, _, reason = r.tfValue(m[2])
		case "copy":
			v, _, reason = r.copyValue(m[2])
		}
		if reason != "" && missing == "" {
			missing = reason
		}
		return v
	})
	if missing != "" {
		return "", OriginNone, missing
	}
	return out, OriginDerived, ""
}

func (r *resolver) resolveKey(secret string, k Entry) (KeyResult, error) {
	kr := KeyResult{Secret: secret, Key: k.Name, Spec: k.Spec, Origin: OriginNone}
	cur, hasCur := r.currentValue(secret, k.Name)
	rotating := r.rotating(secret, k)

	// The never-overwrite rule: a filled key stays as it is unless rotated.
	if hasCur && !rotating {
		kr.Action, kr.Origin, kr.Value = ActKeep, OriginCurrent, cur
		r.driftCheck(secret, k, cur)
		return kr, nil
	}

	var (
		value   string
		origin  Origin
		pending string
		err     error
	)
	switch k.Spec.Kind {
	case KindConst:
		value, origin = k.Spec.Arg, OriginConst
	case KindGenerate:
		value, origin, err = r.ownValue(secret, k, rotating)
	case KindPrompt, KindPromptOptional, KindPromptFile:
		value, origin, pending, err = r.promptValue(secret+"."+k.Name, k.Spec, rotating, func() (string, bool) { return r.pendingValue(secret, k.Name) })
	case KindTF:
		value, origin, pending = r.tfValue(k.Spec.Arg)
	case KindCopy:
		value, origin, pending = r.copyValue(k.Spec.Arg)
	case KindDerive:
		value, origin, pending = r.deriveValue(k.Spec.Arg)
	case KindDSN:
		value, origin, pending, err = r.dsnValue(k.Spec.Arg, rotating)
	case KindShared, KindSharedPrivate, KindSharedPublic:
		sv, serr := r.resolveShared(k.Spec.Arg)
		if serr != nil {
			return kr, serr
		}
		if sv.pending != "" {
			pending = sv.pending
		} else {
			switch k.Spec.Kind {
			case KindShared:
				value = sv.scalar
			case KindSharedPrivate:
				value = sv.pair.Private
			case KindSharedPublic:
				value = sv.pair.Public
			}
			origin = sv.origin
			if origin == OriginGenerated || origin == OriginPrompt {
				origin = OriginDerived
			}
		}
	default:
		return kr, fmt.Errorf("unsupported spec %s", k.Spec)
	}
	if err != nil {
		return kr, err
	}

	prev, hadPrev := r.pendingValue(secret, k.Name)
	switch {
	case pending != "":
		kr.Action, kr.Reason, kr.Value = ActPending, pending, ""
	case value == "":
		kr.Action, kr.Origin, kr.Value = ActEmpty, origin, ""
		if k.Spec.Kind == KindPromptOptional {
			kr.Reason = "optional prompt left empty"
		}
	case hasCur && cur == value:
		kr.Action, kr.Origin, kr.Value = ActKeep, OriginCurrent, value
	case hasCur:
		kr.Action, kr.Origin, kr.Value = ActUpdate, origin, value
	case hadPrev && prev == value:
		kr.Action, kr.Origin, kr.Value = ActReuse, origin, value
	default:
		kr.Action, kr.Origin, kr.Value = ActCreate, origin, value
	}
	return kr, nil
}

// driftCheck warns when a kept copy/tf key no longer matches its source.
func (r *resolver) driftCheck(secret string, k Entry, cur string) {
	var src, hint string
	switch k.Spec.Kind {
	case KindCopy:
		ref, _ := ParseCopyRef(k.Spec.Arg)
		if v, ok := r.in.Sources[ref.Source][ref.Field]; ok && v != "" {
			src, hint = v, "source/"+ref.Source
		}
	case KindTF:
		src, hint = r.in.TF[k.Spec.Arg], secret+"/"+k.Name
	case KindDerive:
		// Compute without recording missing inputs: this is only a check.
		saved := *r.plan
		v, _, reason := r.deriveValue(k.Spec.Arg)
		r.plan.MissingTF, r.plan.MissingSources = saved.MissingTF, saved.MissingSources
		if reason == "" {
			src, hint = v, secret+"/"+k.Name
		}
	default:
		return
	}
	if src != "" && src != cur {
		r.plan.Warnings = append(r.plan.Warnings, fmt.Sprintf("%s.%s differs from its source (%s); --rotate %s re-copies it", secret, k.Name, k.Spec, hint))
	}
}

// ownValue returns a per-secret generated value: the unpushed one, or new.
func (r *resolver) ownValue(secret string, k Entry, rotating bool) (string, Origin, error) {
	if !rotating {
		if v, ok := r.pendingValue(secret, k.Name); ok {
			return v, OriginPending, nil
		}
	}
	v, err := r.in.Gen.Scalar(k.Spec.Arg)
	if err != nil {
		return "", OriginNone, err
	}
	return v, OriginGenerated, nil
}

// promptValue asks the founder when applying; in plan mode it is pending.
// id is "secret.key" or "shared.<name>"; prior finds an unpushed answer.
func (r *resolver) promptValue(id string, spec Spec, rotating bool, prior func() (string, bool)) (string, Origin, string, error) {
	label := spec.Arg
	if !rotating {
		if v, ok := prior(); ok {
			return v, OriginPending, "", nil
		}
	}
	optional := ""
	if spec.Kind == KindPromptOptional {
		optional = " (optional)"
	}
	if !r.in.Apply || r.in.Prompter == nil {
		r.plan.PendingPrompts = append(r.plan.PendingPrompts, id+optional+" — "+label)
		if spec.Kind == KindPromptOptional {
			return "", OriginNone, "", nil
		}
		return "", OriginNone, "prompt not answered (plan mode)", nil
	}
	answer, err := r.in.Prompter.Ask(label)
	if err != nil {
		return "", OriginNone, "", fmt.Errorf("prompt: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if spec.Kind == KindPromptFile && answer != "" {
		b, err := r.in.ReadFile(answer)
		if err != nil {
			// The answer is a path, not a secret, but keep it out anyway.
			return "", OriginNone, "", fmt.Errorf("prompt-file: cannot read the file given for %s", id)
		}
		answer = strings.TrimSpace(string(b))
	}
	if answer == "" {
		r.plan.PendingPrompts = append(r.plan.PendingPrompts, id+optional+" — "+label)
		if spec.Kind == KindPromptOptional {
			return "", OriginPrompt, "", nil
		}
		return "", OriginNone, "prompt left empty", nil
	}
	return answer, OriginPrompt, "", nil
}

func (r *resolver) tfValue(name string) (string, Origin, string) {
	if v, ok := r.in.TF[name]; ok && v != "" {
		return v, OriginTF, ""
	}
	r.plan.MissingTF = append(r.plan.MissingTF, name)
	return "", OriginNone, "terraform output " + name + " not available"
}

func (r *resolver) copyValue(arg string) (string, Origin, string) {
	ref, _ := ParseCopyRef(arg)
	fields, ok := r.in.Sources[ref.Source]
	if !ok {
		r.plan.MissingSources = append(r.plan.MissingSources, ref.Source)
		return "", OriginNone, "source " + ref.Source + " not fetched"
	}
	v := fields[ref.Field]
	if v == "" {
		r.plan.MissingSources = append(r.plan.MissingSources, ref.Source+"#"+ref.Field)
		return "", OriginNone, "source " + ref.Source + " has no " + ref.Field
	}
	return v, OriginCopy, ""
}

// dsnValue assembles postgres://role:pw@host:port/db?sslmode=require.
func (r *resolver) dsnValue(arg string, rotating bool) (string, Origin, string, error) {
	ref, err := ParseDSNRef(arg)
	if err != nil {
		return "", OriginNone, "", err
	}
	pw, err := r.rolePassword(ref.Role, rotating)
	if err != nil {
		return "", OriginNone, "", err
	}
	host, ok := r.in.TF["aurora_cluster_endpoint"]
	if !ok || host == "" {
		r.plan.MissingTF = append(r.plan.MissingTF, "aurora_cluster_endpoint")
		return "", OriginNone, "terraform output aurora_cluster_endpoint not available", nil
	}
	port := r.in.TF["aurora_cluster_port"]
	if port == "" {
		port = "5432"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(ref.Role, pw),
		Host:     host + ":" + port,
		Path:     "/" + ref.Database,
		RawQuery: "sslmode=require",
	}
	return u.String(), OriginDerived, "", nil
}

// rolePassword is the role's password: the one inside a DSN Secrets Manager
// already holds, else the one in an unpushed payload, else a new one.
func (r *resolver) rolePassword(role string, rotating bool) (string, error) {
	if pw, ok := r.plan.RolePasswords[role]; ok {
		return pw, nil
	}
	if !rotating {
		for _, src := range []map[string]map[string]string{r.in.Current, r.in.Pending} {
			if pw, ok := r.adoptRolePassword(role, src); ok {
				r.plan.RolePasswords[role] = pw
				return pw, nil
			}
		}
	}
	pw, err := r.in.Gen.Scalar(GenPassword)
	if err != nil {
		return "", err
	}
	if rotating {
		r.plan.RotatedRoles[role] = true
	}
	r.plan.RolePasswords[role] = pw
	return pw, nil
}

func (r *resolver) adoptRolePassword(role string, src map[string]map[string]string) (string, bool) {
	found := ""
	for _, s := range r.m.Secrets {
		for _, k := range s.Keys {
			if k.Spec.Kind != KindDSN {
				continue
			}
			ref, _ := ParseDSNRef(k.Spec.Arg)
			if ref.Role != role {
				continue
			}
			v, ok := lookup(src, s.Name, k.Name)
			if !ok {
				continue
			}
			u, err := url.Parse(v)
			if err != nil || u.User == nil || u.User.Username() != role {
				continue
			}
			pw, ok := u.User.Password()
			if !ok || pw == "" {
				continue
			}
			if found != "" && found != pw {
				r.plan.Warnings = append(r.plan.Warnings, fmt.Sprintf("role %s has different passwords in different secrets; --rotate role/%s re-aligns them", role, role))
				return found, true
			}
			found = pw
		}
	}
	return found, found != ""
}

// resolveShared returns the shared value, producing it on first use.
func (r *resolver) resolveShared(name string) (*sharedValue, error) {
	if sv, ok := r.shared[name]; ok {
		return sv, nil
	}
	e, ok := r.m.SharedEntry(name)
	if !ok {
		return nil, fmt.Errorf("unknown shared %q", name)
	}
	sv := &sharedValue{entry: e}
	r.shared[name] = sv
	rotated := r.in.Rotate["shared/"+name]

	switch e.Spec.Kind {
	case KindConst:
		sv.scalar, sv.origin = e.Spec.Arg, OriginConst
		return sv, nil
	case KindTF:
		sv.scalar, sv.origin, sv.pending = r.tfValue(e.Spec.Arg)
		return sv, nil
	case KindCopy:
		sv.scalar, sv.origin, sv.pending = r.copyValue(e.Spec.Arg)
		return sv, nil
	}

	// generate / prompt: adopt what Secrets Manager holds, then what an
	// unpushed payload holds, so a re-run never re-keys half the platform.
	if !rotated {
		if r.adoptShared(name, e, sv, r.in.Current, OriginCurrent) || r.adoptShared(name, e, sv, r.in.Pending, OriginPending) {
			return sv, nil
		}
	}
	switch e.Spec.Kind {
	case KindGenerate:
		if e.Spec.IsKeypair() {
			pair, err := r.in.Gen.Pair(e.Spec.Arg)
			if err != nil {
				return nil, err
			}
			sv.pair, sv.origin = pair, OriginGenerated
			r.plan.PublicHalves[name] = pair
			return sv, nil
		}
		v, err := r.in.Gen.Scalar(e.Spec.Arg)
		if err != nil {
			return nil, err
		}
		sv.scalar, sv.origin = v, OriginGenerated
		return sv, nil
	case KindPrompt, KindPromptOptional, KindPromptFile:
		v, origin, pending, err := r.promptValue("shared."+name, e.Spec, true, nil)
		if err != nil {
			return nil, err
		}
		sv.scalar, sv.origin, sv.pending = v, origin, pending
		return sv, nil
	}
	return nil, fmt.Errorf("shared %q: unsupported spec %s", name, e.Spec)
}

// adoptShared seeds a shared value from any secret in src that holds it.
func (r *resolver) adoptShared(name string, e Entry, sv *sharedValue, src map[string]map[string]string, origin Origin) bool {
	for _, s := range r.m.Secrets {
		for _, k := range s.Keys {
			if k.Spec.Arg != name {
				continue
			}
			v, ok := lookup(src, s.Name, k.Name)
			if !ok {
				continue
			}
			switch k.Spec.Kind {
			case KindShared:
				sv.scalar, sv.origin = v, origin
				return true
			case KindSharedPrivate:
				pair, err := pairFromPrivate(e.Spec.Arg, v)
				if err != nil {
					r.plan.Warnings = append(r.plan.Warnings, fmt.Sprintf("%s.%s holds a %s private key this tool cannot parse; shared %s is not adopted from it (%v)", s.Name, k.Name, e.Spec.Arg, name, err))
					continue
				}
				sv.pair, sv.origin = pair, origin
				r.plan.PublicHalves[name] = pair
				return true
			}
		}
	}
	return false
}

// pairFromPrivate rebuilds the public half from a stored private key.
func pairFromPrivate(gen, private string) (Keypair, error) {
	switch gen {
	case GenEd25519:
		raw, err := decodeAnyBase64(private)
		if err != nil {
			return Keypair{}, err
		}
		var priv ed25519.PrivateKey
		switch len(raw) {
		case ed25519.SeedSize:
			priv = ed25519.NewKeyFromSeed(raw)
		case ed25519.PrivateKeySize:
			priv = ed25519.PrivateKey(raw)
		default:
			return Keypair{}, fmt.Errorf("ed25519 key must be 32 or 64 bytes")
		}
		pub := priv.Public().(ed25519.PublicKey)
		return Keypair{Private: private, Public: base64.StdEncoding.EncodeToString(pub)}, nil
	case GenRSA2048:
		block, _ := pem.Decode([]byte(strings.ReplaceAll(private, "\\n", "\n")))
		if block == nil {
			return Keypair{}, fmt.Errorf("not PEM")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			k8, err8 := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err8 != nil {
				return Keypair{}, fmt.Errorf("not an RSA private key")
			}
			rk, ok := k8.(*rsa.PrivateKey)
			if !ok {
				return Keypair{}, fmt.Errorf("PKCS8 key is not RSA")
			}
			key = rk
		}
		pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return Keypair{}, err
		}
		pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
		return Keypair{Private: private, Public: string(pubPEM)}, nil
	}
	return Keypair{}, fmt.Errorf("unknown keypair generator %q", gen)
}

func decodeAnyBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not base64")
}

// verifyPairs is the pairing guard: for every shared keypair, every private
// half in the plan (kept, reused or new) must produce the public half every
// receiver gets. A receiver keeping an old public key after the caller was
// re-keyed, or two callers holding different seeds, is a mismatch.
func (r *resolver) verifyPairs() {
	for _, e := range r.m.Shared {
		if !e.Spec.IsKeypair() {
			continue
		}
		var privs, pubs []KeyResult
		for _, s := range r.plan.Secrets {
			for _, k := range s.Keys {
				if k.Spec.Arg != e.Name || k.Value == "" {
					continue
				}
				switch k.Spec.Kind {
				case KindSharedPrivate:
					privs = append(privs, k)
				case KindSharedPublic:
					pubs = append(pubs, k)
				}
			}
		}
		want := ""
		wantFrom := ""
		var bad []string
		for _, k := range privs {
			pair, err := pairFromPrivate(e.Spec.Arg, k.Value)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s.%s is not a valid %s private key", k.Secret, k.Key, e.Spec.Arg))
				continue
			}
			if want == "" {
				want, wantFrom = strings.TrimSpace(pair.Public), k.Secret+"."+k.Key
			} else if strings.TrimSpace(pair.Public) != want {
				bad = append(bad, fmt.Sprintf("%s.%s and %s hold different private keys", k.Secret, k.Key, wantFrom))
			}
		}
		for _, k := range pubs {
			if want == "" {
				want, wantFrom = strings.TrimSpace(k.Value), k.Secret+"."+k.Key
				continue
			}
			if strings.TrimSpace(k.Value) != want {
				bad = append(bad, fmt.Sprintf("%s.%s (%s) does not match %s", k.Secret, k.Key, k.Action, wantFrom))
			}
		}
		if len(bad) > 0 {
			r.plan.PairErrors = append(r.plan.PairErrors, fmt.Sprintf("keypair %s: %s. Run with --rotate shared/%s to re-key every holder together.", e.Name, strings.Join(bad, "; "), e.Name))
		}
	}
}

func (r *resolver) collectRoles() {
	seen := map[string]bool{}
	for _, s := range r.m.Secrets {
		for _, k := range s.Keys {
			if k.Spec.Kind != KindDSN {
				continue
			}
			ref, _ := ParseDSNRef(k.Spec.Arg)
			id := ref.Role + "@" + ref.Database
			if seen[id] {
				continue
			}
			seen[id] = true
			r.plan.Roles = append(r.plan.Roles, RoleGrant{Role: ref.Role, Database: ref.Database})
		}
	}
	sort.Slice(r.plan.Roles, func(i, j int) bool {
		if r.plan.Roles[i].Database != r.plan.Roles[j].Database {
			return r.plan.Roles[i].Database < r.plan.Roles[j].Database
		}
		return r.plan.Roles[i].Role < r.plan.Roles[j].Role
	})
}

func sortedBools(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	out := in[:0]
	var last string
	for i, s := range in {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

// adoptKeptRolePasswords gives roles.sql the password of every role whose
// DSNs were all kept (resolveKey never derived them). It only ADOPTS the
// password inside the DSN Secrets Manager holds: generating one here would
// set a password no service connects with.
func (r *resolver) adoptKeptRolePasswords() {
	for _, g := range r.plan.Roles {
		if _, ok := r.plan.RolePasswords[g.Role]; ok {
			continue
		}
		for _, src := range []map[string]map[string]string{r.in.Current, r.in.Pending} {
			if pw, ok := r.adoptRolePassword(g.Role, src); ok {
				r.plan.RolePasswords[g.Role] = pw
				break
			}
		}
		if _, ok := r.plan.RolePasswords[g.Role]; !ok {
			r.plan.Warnings = append(r.plan.Warnings, fmt.Sprintf("role %s: its DSN in Secrets Manager carries no readable password; roles.sql leaves the role alone (--rotate role/%s sets a new one everywhere)", g.Role, g.Role))
		}
	}
}
