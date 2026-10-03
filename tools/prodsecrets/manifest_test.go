package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const testManifest = `
version: 1
prefix: atpost/test
sources:
  platform-auth: atpost/test/platform-auth
  msk-scram: tf:msk_scram_secret_name
shared:
  jwt_prev: const:
  svc_commerce: generate:ed25519-keypair
  svc_commerce_kid: const:c1
  mini_rs256: generate:rsa2048-pem
  razorpay_key_id: prompt:Razorpay key id
secrets:
  commerce-service:
    postgres_dsn: dsn:commerce_db/commerce_service
    internal_service_key: copy:platform-auth#internal_service_key
    jwt_kid_previous: shared:jwt_prev
    commerce_service_token_key: shared-private:svc_commerce
    commerce_service_token_kid: shared:svc_commerce_kid
    commerce_pii_lookup_salt: generate:hex64
    shiprocket_email: prompt-optional:Shiprocket email
    commerce_kms_key_id: tf:commerce_pii_kms_key_id
    courier_provider: const:stub
  payments-service:
    postgres_dsn: dsn:commerce_db/payments_service
    internal_service_key: copy:platform-auth#internal_service_key
    caller_commerce_pubkey: shared-public:svc_commerce
    caller_commerce_kid: shared:svc_commerce_kid
    razorpay_key_id: shared:razorpay_key_id
    jwt_public_key_pem: copy:platform-auth#jwt_public_key_pem
    kafka_sasl_password: copy:msk-scram#password
    redis_addr: derive:{tf:elasticache_primary_endpoint}:6379
  identity-auth-service:
    postgres_dsn: dsn:identity_db/identity_auth
    mini_app_session_private_key_pem: shared-private:mini_rs256
    totp_encryption_key: generate:totp-hex64
    fcm_service_account_key: prompt-file:Path to the Firebase JSON
  empty-shell:
`

func mustParse(t *testing.T, src string) *Manifest {
	t.Helper()
	m, err := ParseManifest(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

func TestParseManifestSubset(t *testing.T) {
	m := mustParse(t, testManifest)
	if m.Prefix != "atpost/test" || m.Version != 1 {
		t.Fatalf("header: %+v", m)
	}
	if len(m.Shared) != 5 || len(m.Sources) != 2 {
		t.Fatalf("shared %d, sources %d", len(m.Shared), len(m.Sources))
	}
	if got := m.SecretNames(); strings.Join(got, ",") != "commerce-service,payments-service,identity-auth-service,empty-shell" {
		t.Fatalf("secret order: %v", got)
	}
	if m.Secrets[3].Keys != nil {
		t.Fatalf("empty-shell must have no keys")
	}
	k := m.Secrets[0].Keys[0]
	if k.Name != "postgres_dsn" || k.Spec.Kind != KindDSN || k.Spec.Arg != "commerce_db/commerce_service" {
		t.Fatalf("first key: %+v", k)
	}
	if e, _ := m.SharedEntry("jwt_prev"); e.Spec.Kind != KindConst || e.Spec.Arg != "" {
		t.Fatalf("empty const: %+v", e)
	}
	src, _ := m.SourceByAlias("msk-scram")
	if id, ok := src.SecretID(map[string]string{"msk_scram_secret_name": "AmazonMSK_x"}); !ok || id != "AmazonMSK_x" {
		t.Fatalf("tf source id: %q %v", id, ok)
	}
	if _, ok := src.SecretID(nil); ok {
		t.Fatal("unresolved tf source must report missing")
	}
	if m.FullName("web") != "atpost/test/web" {
		t.Fatal("FullName")
	}
	_, keys, byCat := m.Summary()
	if keys != 21 || byCat["copy"] != 4 || byCat["derive"] != 4 || byCat["generate"] != 5 || byCat["prompt"] != 3 || byCat["terraform-output"] != 1 || byCat["literal"] != 4 {
		t.Fatalf("summary: %d %v", keys, byCat)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string]string{
		"tabs":                    "version: 1\nprefix: p\nsecrets:\n\tx: const:a\n",
		"lists":                   "version: 1\nprefix: p\nsecrets:\n  - x\n",
		"duplicate key":           "version: 1\nprefix: p\nshared:\n  a: const:x\n  a: const:y\n",
		"unknown kind":            "version: 1\nprefix: p\nshared:\n  a: magic:x\n",
		"unknown generator":       "version: 1\nprefix: p\nshared:\n  a: generate:hex128\n",
		"keypair in secret":       "version: 1\nprefix: p\nsecrets:\n  s:\n    k: generate:ed25519-keypair\n",
		"unknown shared":          "version: 1\nprefix: p\nsecrets:\n  s:\n    k: shared:nope\n",
		"scalar ref to pair":      "version: 1\nprefix: p\nshared:\n  kp: generate:ed25519-keypair\nsecrets:\n  s:\n    p: shared-private:kp\n    k: shared:kp\n",
		"pair ref to scalar":      "version: 1\nprefix: p\nshared:\n  v: generate:hex64\nsecrets:\n  s:\n    k: shared-public:v\n",
		"keypair without holder":  "version: 1\nprefix: p\nshared:\n  kp: generate:ed25519-keypair\nsecrets:\n  s:\n    k: shared-public:kp\n",
		"bad dsn":                 "version: 1\nprefix: p\nsecrets:\n  s:\n    k: dsn:App/role\n",
		"shared cannot deref":     "version: 1\nprefix: p\nshared:\n  a: shared:b\n",
		"missing prefix":          "version: 1\nsecrets:\n  s:\n    k: const:x\n",
		"odd indent":              "version: 1\nprefix: p\nsecrets:\n   s:\n    k: const:x\n",
		"nested under scalar":     "version: 1\nprefix: p\nshared:\n  a: const:x\n    b: const:y\n",
		"prompt without label":    "version: 1\nprefix: p\nshared:\n  a: prompt:\n",
		"copy unknown source":     "version: 1\nprefix: p\nsecrets:\n  s:\n    k: copy:nowhere#f\n",
		"copy without field":      "version: 1\nprefix: p\nsources:\n  a: x/y\nsecrets:\n  s:\n    k: copy:a\n",
		"derive no placeholder":   "version: 1\nprefix: p\nsecrets:\n  s:\n    k: derive:plain\n",
		"derive bad placeholder":  "version: 1\nprefix: p\nsecrets:\n  s:\n    k: derive:{env:HOME}\n",
		"derive unknown source":   "version: 1\nprefix: p\nsecrets:\n  s:\n    k: derive:x{copy:nope#f}\n",
		"duplicate source":        "version: 1\nprefix: p\nsources:\n  a: x\n  a: y\n",
		"empty source":            "version: 1\nprefix: p\nsources:\n  a:\n",
		"derive in shared":        "version: 1\nprefix: p\nshared:\n  a: derive:{tf:x}\n",
		"dsn in shared":           "version: 1\nprefix: p\nshared:\n  a: dsn:app/r\n",
		"secret is a scalar":      "version: 1\nprefix: p\nsecrets:\n  s: const:x\n",
		"unknown top level":       "version: 1\nprefix: p\nextra:\n  a: b\n",
		"wrong version":           "version: 2\nprefix: p\n",
		"bad tf source":           "version: 1\nprefix: p\nsources:\n  a: tf:bad name\n",
		"duplicate secret":        "version: 1\nprefix: p\nsecrets:\n  s:\n    a: const:x\n  s:\n    b: const:y\n",
		"derive unbalanced brace": "version: 1\nprefix: p\nsecrets:\n  s:\n    k: derive:{tf:x}}\n",
	}
	for name, src := range cases {
		if _, err := ParseManifest(strings.NewReader(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseQuotedAndCommentedValues(t *testing.T) {
	m := mustParse(t, "# top comment\nversion: 1\nprefix: p\nshared:\n  # inside\n  a: \"const:x: y\"\n  b: 'const:'\n")
	a, _ := m.SharedEntry("a")
	b, _ := m.SharedEntry("b")
	if a.Spec.Arg != "x: y" || b.Spec.Arg != "" {
		t.Fatalf("quoted values: %+v %+v", a, b)
	}
}

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{"..", ".."}, parts...)...)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not found (running outside the repo)", p)
	}
	return p
}

func loadCommitted(t *testing.T, env string) *Manifest {
	t.Helper()
	m, err := LoadManifest("manifest.yaml", env)
	if err != nil {
		t.Fatalf("manifest.yaml (%s): %v", env, err)
	}
	if m.Prefix != "atpost/"+env {
		t.Fatalf("prefix %q", m.Prefix)
	}
	return m
}

// forEachEnv runs f once per known environment as a subtest.
func forEachEnv(t *testing.T, f func(t *testing.T, env string)) {
	for _, env := range KnownEnvs {
		env := env
		t.Run(env, func(t *testing.T) { f(t, env) })
	}
}

// envTerraformDir is infra/terraform/envs/<env>. Production must exist; any
// other environment is skipped until its directory is created (the QA
// Terraform lands separately), and checked fully from then on.
func envTerraformDir(t *testing.T, env string) string {
	t.Helper()
	dir := repoPath(t, "infra", "terraform", "envs", "prod")
	dir = filepath.Join(filepath.Dir(dir), env)
	if _, err := os.Stat(dir); err != nil {
		if env == DefaultEnv {
			t.Fatalf("%s missing", dir)
		}
		t.Skipf("%s does not exist yet", dir)
	}
	return dir
}

func valuesFiles(t *testing.T, env string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(repoPath(t, "deploy", "services"), "*", "values-"+env+".yaml"))
	web, _ := filepath.Glob(filepath.Join(repoPath(t, "deploy", "web"), "*", "values-"+env+".yaml"))
	return append(files, web...)
}

// The committed manifest must load, and every externalSecret remoteRef in
// deploy/services/*/values-<env>.yaml and deploy/web/*/values-<env>.yaml must
// exist under the matching secret: the drift guard between the chart values
// and the seeder, for production and for every other environment whose
// values files exist (QA: skipped while there is no values-qa.yaml, failing
// on the first remoteRef the manifest lacks once there is). Commented-out
// refs are checked too (they are what the next enablement uncomments).
// deploy/argocd/*.yaml remoteRefs (the repository credential) are checked the
// same way.
func TestCommittedManifestCoversDeployValues(t *testing.T) {
	prodFiles := 0
	forEachEnv(t, func(t *testing.T, env string) {
		m := loadCommitted(t, env)
		keysOf := map[string]map[string]bool{}
		for _, s := range m.Secrets {
			keysOf[s.Name] = map[string]bool{}
			for _, k := range s.Keys {
				keysOf[s.Name][k.Name] = true
			}
		}
		omitted := map[string]bool{}
		for _, o := range m.Omitted {
			omitted[o] = true
		}
		files := valuesFiles(t, env)
		if env == DefaultEnv {
			prodFiles = len(files)
		} else if len(files) == 0 {
			t.Skipf("no deploy/**/values-%s.yaml yet", env)
		}
		prefix := regexp.QuoteMeta(m.Prefix)
		remoteKeyRe := regexp.MustCompile(`^\s*remoteKey:\s*` + prefix + `/([A-Za-z0-9-]+)\s*$`)
		refRe := regexp.MustCompile(`^\s*#?\s*-\s*\{\s*secretKey:\s*[A-Z0-9_]+\s*,\s*remoteRef:\s*([a-z0-9_]+)\s*(,\s*remoteKey:\s*([^\s}]+)\s*)?\}`)
		checked := 0
		for _, f := range files {
			fh, err := os.Open(f)
			if err != nil {
				t.Fatal(err)
			}
			var secret string
			sc := bufio.NewScanner(fh)
			for sc.Scan() {
				line := sc.Text()
				if mm := remoteKeyRe.FindStringSubmatch(line); mm != nil {
					secret = mm[1]
					if _, ok := keysOf[secret]; !ok {
						t.Errorf("%s: secret %q is not in manifest.yaml for %s", f, secret, env)
					}
					continue
				}
				mm := refRe.FindStringSubmatch(line)
				if mm == nil || secret == "" {
					continue
				}
				if mm[3] != "" && mm[3] != m.Prefix+"/"+secret {
					continue // a per-entry remoteKey reads another (Terraform-owned) secret
				}
				checked++
				if !keysOf[secret][mm[1]] {
					t.Errorf("%s: remoteRef %q is not a key of %s in manifest.yaml (%s)", f, mm[1], secret, env)
				}
			}
			fh.Close()
		}
		// A partial set (another lane still writing values-qa.yaml) is
		// checked ref by ref; the parser sanity floor applies to full sets.
		if len(files) >= prodFiles && checked < 300 {
			t.Fatalf("only %d remoteRefs checked in %d files; the values parser is probably broken", checked, len(files))
		}
		if len(files) < prodFiles {
			t.Logf("%s: %d of %d values files exist; %d remoteRefs checked", env, len(files), prodFiles, checked)
		}

		// ExternalSecrets outside the chart: `key: <prefix>/<secret>` then
		// `property: <key>`.
		argo, _ := filepath.Glob(filepath.Join(repoPath(t, "deploy", "argocd"), "*.yaml"))
		argoSub, _ := filepath.Glob(filepath.Join(repoPath(t, "deploy", "argocd"), "*", "*.yaml"))
		keyRe := regexp.MustCompile(`^\s*key:\s*` + prefix + `/([A-Za-z0-9-]+)\s*$`)
		propRe := regexp.MustCompile(`^\s*property:\s*([A-Za-z0-9_.-]+)\s*$`)
		for _, f := range append(argo, argoSub...) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var secret string
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimRight(line, "\r")
				if mm := keyRe.FindStringSubmatch(line); mm != nil {
					secret = mm[1]
					continue
				}
				if mm := propRe.FindStringSubmatch(line); mm != nil && secret != "" {
					switch {
					case omitted[secret]:
						t.Logf("%s: %s/%s.%s is omitted in %s (no Terraform shell yet)", f, m.Prefix, secret, mm[1], env)
					case !keysOf[secret][mm[1]]:
						t.Errorf("%s: %s/%s property %q is not in manifest.yaml (%s)", f, m.Prefix, secret, mm[1], env)
					}
					secret = ""
				}
			}
		}
	})
}

// terraformText is every *.tf of one environment, concatenated.
func terraformText(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	var b strings.Builder
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(body)
		b.WriteString("\n")
	}
	return b.String()
}

// The seeder fills exactly the shells Terraform creates: service_names +
// web_image_names in infra/terraform/envs/<env>, plus any other shell that
// environment's Terraform names explicitly (the ArgoCD repository
// credential). QA is checked once envs/qa exists.
func TestCommittedManifestMatchesTerraformShells(t *testing.T) {
	forEachEnv(t, func(t *testing.T, env string) {
		dir := envTerraformDir(t, env)
		m := loadCommitted(t, env)
		src := terraformText(t, dir)
		listOf := func(name string) []string {
			re := regexp.MustCompile(`(?s)variable "` + name + `" \{.*?default\s*=\s*\[(.*?)\]`)
			mm := re.FindStringSubmatch(src)
			if mm == nil {
				t.Fatalf("envs/%s: no default list for %s", env, name)
			}
			var out []string
			for _, q := range regexp.MustCompile(`"([a-z0-9-]+)"`).FindAllStringSubmatch(mm[1], -1) {
				out = append(out, q[1])
			}
			return out
		}
		want := append(listOf("service_names"), listOf("web_image_names")...)
		sort.Strings(want)
		inTF := map[string]bool{}
		for _, w := range want {
			inTF[w] = true
		}
		got := m.SecretNames()
		sort.Strings(got)
		inManifest := map[string]bool{}
		for _, g := range got {
			inManifest[g] = true
		}
		for _, w := range want {
			if !inManifest[w] {
				t.Errorf("envs/%s creates a shell for %s, which the manifest does not fill", env, w)
			}
		}
		for _, g := range got {
			if inTF[g] {
				continue
			}
			// Not a service shell: that environment's Terraform must create
			// it by name, or the fetch before every apply fails.
			if !strings.Contains(src, `"`+g+`"`) && !strings.Contains(src, "/"+g+`"`) {
				t.Errorf("manifest fills %s/%s, but envs/%s creates no such secret", m.Prefix, g, env)
			}
		}
		if len(want) != 34 {
			t.Fatalf("expected 32 services + web + admin-console in envs/%s, got %d", env, len(want))
		}
	})
}

// Every tf: name the manifest reads is a real output of envs/<env>, and
// every copy:<source>#<field> is a field Terraform writes into that source.
func TestCommittedManifestReadsRealTerraform(t *testing.T) {
	forEachEnv(t, func(t *testing.T, env string) {
		dir := envTerraformDir(t, env)
		m := loadCommitted(t, env)
		outputs := map[string]bool{}
		outRe := regexp.MustCompile(`(?m)^output "([a-z0-9_]+)"`)
		for _, mm := range outRe.FindAllStringSubmatch(terraformText(t, dir), -1) {
			outputs[mm[1]] = true
		}
		needTF := map[string]bool{"aurora_cluster_endpoint": true} // dsn:
		moduleOf := map[string]string{
			"platform-auth":      "auth-keys",
			"elasticache-auth":   "elasticache",
			"opensearch-master":  "opensearch",
			"msk-scram":          "msk",
			"cloudfront-signing": "media",
		}
		fields := map[string]map[string]bool{}
		for _, s := range m.Sources {
			if out, ok := s.TFOutput(); ok {
				needTF[out] = true
			}
			mod, ok := moduleOf[s.Alias]
			if !ok {
				t.Errorf("source %s: test does not know its Terraform module", s.Alias)
				continue
			}
			b, err := os.ReadFile(repoPath(t, "infra", "terraform", "modules", mod, "main.tf"))
			if err != nil {
				t.Fatal(err)
			}
			body := string(b)
			if !strings.HasPrefix(s.Ref, "tf:") {
				// atpost/<env>/<x> must be the name the module creates.
				name := strings.Replace(s.Ref, m.Prefix+"/", "atpost/${var.environment}/", 1)
				if !strings.Contains(body, `"`+name+`"`) {
					t.Errorf("source %s: module %s does not create %q", s.Alias, mod, s.Ref)
				}
			}
			js := regexp.MustCompile(`(?s)secret_string\s*=\s*jsonencode\(\{(.*?)\}\)`).FindStringSubmatch(body)
			if js == nil {
				t.Fatalf("module %s: no jsonencode secret_string", mod)
			}
			fields[s.Alias] = map[string]bool{}
			for _, f := range regexp.MustCompile(`(?m)^\s*([a-z0-9_]+)\s*=`).FindAllStringSubmatch(js[1], -1) {
				fields[s.Alias][f[1]] = true
			}
		}
		checkCopy := func(where, arg string) {
			ref, _ := ParseCopyRef(arg)
			if !fields[ref.Source][ref.Field] {
				t.Errorf("%s: %s has no field %q in Terraform", where, ref.Source, ref.Field)
			}
		}
		for _, s := range m.Secrets {
			for _, k := range s.Keys {
				where := s.Name + "." + k.Name
				switch k.Spec.Kind {
				case KindTF:
					needTF[k.Spec.Arg] = true
				case KindCopy:
					checkCopy(where, k.Spec.Arg)
				case KindDerive:
					for _, p := range DerivePlaceholders(k.Spec.Arg) {
						if p[0] == "tf" {
							needTF[p[1]] = true
						} else {
							checkCopy(where, p[1])
						}
					}
				}
			}
		}
		for name := range needTF {
			if !outputs[name] {
				t.Errorf("terraform output %q is not defined in infra/terraform/envs/%s", name, env)
			}
		}
	})
}

// Every service-token keypair has exactly one caller (private holder) and
// the platform secret is never written by the seeder.
func TestCommittedManifestPairing(t *testing.T) {
	forEachEnv(t, func(t *testing.T, env string) {
		m := loadCommitted(t, env)
		holders := map[string][]string{}
		receivers := map[string]int{}
		for _, s := range m.Secrets {
			if s.Name == "platform-auth" {
				t.Fatal("platform-auth is Terraform's; the seeder must only read it")
			}
			for _, k := range s.Keys {
				switch k.Spec.Kind {
				case KindSharedPrivate:
					holders[k.Spec.Arg] = append(holders[k.Spec.Arg], s.Name)
				case KindSharedPublic:
					receivers[k.Spec.Arg]++
				}
			}
		}
		for _, e := range m.Shared {
			if !e.Spec.IsKeypair() {
				continue
			}
			if len(holders[e.Name]) != 1 {
				t.Errorf("keypair %s: private half in %v, want exactly one caller", e.Name, holders[e.Name])
			}
			if strings.HasPrefix(e.Name, "svc_") {
				if receivers[e.Name] == 0 {
					t.Errorf("service token %s has no receiver", e.Name)
				}
				if _, ok := m.SharedEntry(e.Name + "_kid"); !ok {
					t.Errorf("service token %s has no %s_kid", e.Name, e.Name)
				}
			}
		}
	})
}

// A full apply of the committed manifest with stand-in Terraform values and
// every required prompt answered: nothing pending, every pair verifies, every
// role has a password and every DSN points at the role's own database.
func TestCommittedManifestFullApply(t *testing.T) {
	forEachEnv(t, func(t *testing.T, env string) {
		m := loadCommitted(t, env)
		pair, _ := Generator{}.Pair(GenRSA2048)
		in := Inputs{
			Apply: true,
			TF: map[string]string{
				"aurora_cluster_endpoint":      "aurora.example.internal",
				"commerce_pii_kms_key_id":      "1234abcd-12ab-34cd-56ef-1234567890ab",
				"elasticache_primary_endpoint": "cache.example.internal",
				"msk_bootstrap_brokers":        "b-1.example:9096,b-2.example:9096",
				"opensearch_endpoint":          "vpc-search.example.internal",
				"msk_scram_secret_name":        "AmazonMSK_atpost-" + env + "-atpost",
			},
			Sources: map[string]map[string]string{
				"platform-auth":      {"jwt_secret": "JWTSECRETSTANDIN000000000000000001", "jwt_kid": "v1", "internal_service_key": "INTERNALKEYSTANDIN0000000000001", "jwt_private_key_pem": pair.Private, "jwt_public_key_pem": pair.Public, "jwt_rs256_kid": "rsa-1"},
				"elasticache-auth":   {"auth_token": "REDISAUTHSTANDIN000000000001"},
				"opensearch-master":  {"username": "atpost-master", "password": "OSPASSWORDSTANDIN0000001"},
				"msk-scram":          {"username": "atpost", "password": "SCRAMSTANDIN000000000001"},
				"cloudfront-signing": {"media_cloudfront_key_pair_id": "K2STANDIN", "media_cloudfront_private_key": pair.Private, "media_cdn_base_url": "https://" + m.Vars["media_host"]},
			},
			Prompter: answerRequired{},
			ReadFile: func(string) ([]byte, error) { return []byte(`{"type":"service_account"}`), nil },
		}
		p, err := Resolve(m, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.MissingTF)+len(p.MissingSources)+len(p.PairErrors)+len(p.PrefixErrors)+len(p.Warnings) != 0 {
			t.Fatalf("missing tf %v, sources %v, pairs %v, prefixes %v, warnings %v", p.MissingTF, p.MissingSources, p.PairErrors, p.PrefixErrors, p.Warnings)
		}
		for _, s := range p.Secrets {
			for _, k := range s.Keys {
				if k.Action == ActPending {
					t.Errorf("%s.%s pending: %s", s.Name, k.Key, k.Reason)
				}
				if k.Spec.Kind == KindDSN {
					ref, _ := ParseDSNRef(k.Spec.Arg)
					if !strings.HasSuffix(strings.SplitN(k.Value, "?", 2)[0], "/"+ref.Database) || !strings.Contains(k.Value, ref.Role+":"+p.RolePasswords[ref.Role]+"@") {
						t.Errorf("%s.%s: DSN does not carry %s on %s", s.Name, k.Key, ref.Role, ref.Database)
					}
				}
			}
		}
		for _, g := range p.Roles {
			if len(p.RolePasswords[g.Role]) != 32 {
				t.Errorf("role %s has no password", g.Role)
			}
		}
		if len(p.Roles) != 32 {
			t.Errorf("roles: %d", len(p.Roles))
		}
		// Spot checks of the cross-service contracts.
		get := func(secret, key string) string { return keyOf(t, p, secret, key).Value }
		if get("web", "chat_proxy_signing_secret") != get("chat-ws-gateway", "jwt_secret") {
			t.Error("the web chat proxy must sign with the platform jwt_secret")
		}
		if get("post-service", "post_moderation_hmac_key") != get("trust-safety-service", "post_moderation_hmac_key") {
			t.Error("moderation HMAC differs between issuer and verifier")
		}
		if get("search-service", "opensearch_url") != "https://vpc-search.example.internal" || get("media-service", "redis_addr") != "cache.example.internal:6379" {
			t.Error("derived addresses")
		}
		wantPrefix, _ := requiredPrefix(env, "razorpay_key_id")
		for _, s := range []string{"payments-service", "monetization-service"} {
			if v := get(s, "razorpay_key_id"); !strings.HasPrefix(v, wantPrefix) {
				t.Errorf("%s.razorpay_key_id is not a %s key in %s", s, wantPrefix, env)
			}
		}
		for _, c := range [][3]string{
			{"live-service-v2", "live_service_token_privkey", "media-service.caller_live_pubkey"},
			{"commerce-service", "commerce_service_token_key", "payments-service.caller_commerce_pubkey"},
			{"food-service", "food_service_token_key", "payments-service.caller_food_pubkey"},
			{"admin-service", "admin_service_token_key", "user-service.caller_admin_pubkey"},
			{"post-service", "post_service_token_key", "trust-safety-service.caller_post_pubkey"},
			{"trust-safety-service", "trust_safety_service_token_key", "post-service.caller_trust_safety_pubkey"},
		} {
			recv := strings.SplitN(c[2], ".", 2)
			rebuilt, err := pairFromPrivate(GenEd25519, get(c[0], c[1]))
			if err != nil || rebuilt.Public != get(recv[0], recv[1]) {
				t.Errorf("%s.%s does not pair with %s", c[0], c[1], c[2])
			}
		}
	})
}

// answerRequired answers every prompt with a stand-in value; a Razorpay key
// id gets the mode prefix its label asks for.
type answerRequired struct{}

func (answerRequired) Ask(label string) (string, error) {
	if strings.HasPrefix(label, "Path to") {
		return "/stand-in/path.json", nil
	}
	for _, p := range []string{"rzp_live_", "rzp_test_"} {
		if strings.Contains(label, "("+p) {
			return p + "STANDIN0123456789", nil
		}
	}
	return "STANDIN-" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, label)[:8], nil
}
