package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestReplicationClause(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		want    string
		wantErr string
	}{
		{name: "dev default", cfg: DevConfig, want: "{'class': 'SimpleStrategy', 'replication_factor': 1}"},
		{name: "prod", cfg: ProdConfig, want: "{'class': 'NetworkTopologyStrategy', 'replication_factor': 3}"},
		{name: "strategy name is case-insensitive and canonicalised",
			cfg: Config{Strategy: "networktopologystrategy", Factor: 3}, want: "{'class': 'NetworkTopologyStrategy', 'replication_factor': 3}"},
		{name: "explicit datacenters, sorted",
			cfg:  Config{Strategy: NetworkTopologyStrategy, Factor: 3, Datacenters: []Datacenter{{"mumbai", 3}, {"dr", 2}}},
			want: "{'class': 'NetworkTopologyStrategy', 'dr': 2, 'mumbai': 3}"},
		{name: "unknown strategy", cfg: Config{Strategy: "EverywhereStrategy", Factor: 3}, wantErr: "is not SimpleStrategy or NetworkTopologyStrategy"},
		{name: "zero factor", cfg: Config{Strategy: SimpleStrategy, Factor: 0}, wantErr: "must be >= 1"},
		{name: "datacenters with SimpleStrategy", cfg: Config{Strategy: SimpleStrategy, Factor: 1, Datacenters: []Datacenter{{"dc1", 1}}}, wantErr: "only meaningful with NetworkTopologyStrategy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.ReplicationClause()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("clause = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestConfigFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{name: "unset is today's dev values", env: map[string]string{}, want: DevConfig},
		{name: "production", env: map[string]string{EnvStrategy: "NetworkTopologyStrategy", EnvFactor: "3"}, want: ProdConfig},
		{name: "factor alone", env: map[string]string{EnvFactor: "2"}, want: Config{Strategy: SimpleStrategy, Factor: 2}},
		{name: "datacenters inherit the factor",
			env:  map[string]string{EnvStrategy: "NetworkTopologyStrategy", EnvFactor: "3", EnvDatacenters: "ap-south-1"},
			want: Config{Strategy: NetworkTopologyStrategy, Factor: 3, Datacenters: []Datacenter{{"ap-south-1", 3}}}},
		{name: "datacenters with explicit factors",
			env:  map[string]string{EnvStrategy: "NetworkTopologyStrategy", EnvFactor: "3", EnvDatacenters: "dc1:3, dc2:2"},
			want: Config{Strategy: NetworkTopologyStrategy, Factor: 3, Datacenters: []Datacenter{{"dc1", 3}, {"dc2", 2}}}},
		{name: "non-integer factor", env: map[string]string{EnvFactor: "three"}, wantErr: "not an integer"},
		{name: "factor below one", env: map[string]string{EnvFactor: "0"}, wantErr: "must be >= 1"},
		{name: "bad strategy", env: map[string]string{EnvStrategy: "LocalStrategy"}, wantErr: "is not SimpleStrategy"},
		{name: "duplicate datacenter", env: map[string]string{EnvStrategy: "NetworkTopologyStrategy", EnvDatacenters: "a:1,a:2"}, wantErr: "listed twice"},
		{name: "datacenter name with a quote", env: map[string]string{EnvStrategy: "NetworkTopologyStrategy", EnvDatacenters: "a'b"}, wantErr: "not a datacenter name"},
		{name: "datacenters on SimpleStrategy", env: map[string]string{EnvDatacenters: "dc1"}, wantErr: "only meaningful"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConfigFromEnv(envOf(tc.env))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("config = %+v, want %+v", got, tc.want)
			}
		})
	}
}

const sampleCRLF = "-- Feed\r\nCREATE KEYSPACE IF NOT EXISTS social_feed WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};\r\n\r\n" +
	"CREATE TABLE IF NOT EXISTS social_feed.t (\r\n    id uuid,\r\n    PRIMARY KEY (id)\r\n) WITH default_time_to_live = 604800;\r\n\r\n" +
	"CREATE KEYSPACE IF NOT EXISTS chatservice\r\n  WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};\r\n"

func TestRenderRewritesOnlyTheReplicationMap(t *testing.T) {
	out, names, err := Render([]byte(sampleCRLF), ProdConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"social_feed", "chatservice"}) {
		t.Fatalf("names = %v", names)
	}
	want := "-- Feed\r\nCREATE KEYSPACE IF NOT EXISTS social_feed WITH replication = {'class': 'NetworkTopologyStrategy', 'replication_factor': 3};\r\n\r\n" +
		"CREATE TABLE IF NOT EXISTS social_feed.t (\r\n    id uuid,\r\n    PRIMARY KEY (id)\r\n) WITH default_time_to_live = 604800;\r\n\r\n" +
		"CREATE KEYSPACE IF NOT EXISTS chatservice\r\n  WITH replication = {'class': 'NetworkTopologyStrategy', 'replication_factor': 3};\r\n"
	if string(out) != want {
		t.Fatalf("render mismatch\n got: %q\nwant: %q", out, want)
	}
	if strings.Contains(string(out), "SimpleStrategy") {
		t.Fatal("SimpleStrategy survived the production render")
	}
	// The table's own WITH clause and its CRLF line endings are untouched.
	if !strings.Contains(string(out), "WITH default_time_to_live = 604800;\r\n") {
		t.Fatal("table WITH clause or CRLF endings were altered")
	}
}

func TestRenderIsIdempotentAndRoundTrips(t *testing.T) {
	prod, _, err := Render([]byte(sampleCRLF), ProdConfig)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := Render(prod, ProdConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prod, again) {
		t.Fatal("second render changed the output")
	}
	back, _, err := Render(prod, DevConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != sampleCRLF {
		t.Fatalf("dev render of the prod file is not the dev file\n got: %q", back)
	}
}

func TestRenderRefusesFileWithoutKeyspaces(t *testing.T) {
	if _, _, err := Render([]byte("CREATE TABLE IF NOT EXISTS x.y (id uuid PRIMARY KEY);"), ProdConfig); err == nil {
		t.Fatal("expected an error for a file with no CREATE KEYSPACE")
	}
}

// repoFile reads a path relative to Architecture/ (this package lives at
// Architecture/tools/scyllaschema).
func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return b
}

// The committed per-environment schemas must be exactly the render of the dev
// schema for that environment: same tables, same bytes, only the replication
// maps differ. prod = NetworkTopologyStrategy RF 3; qa = the same strategy at
// RF 1 (one Scylla node in the QA account).
func TestCommittedEnvSchemasAreCurrent(t *testing.T) {
	chat := filepath.Join("..", "chat-service", "services", "message-service", "scylla")
	for _, env := range []struct {
		name   string
		cfg    Config
		suffix string
		rf     string
	}{
		{"prod", ProdConfig, "schema.prod.cql", "'replication_factor': 3"},
		{"qa", QAConfig, "schema.qa.cql", "'replication_factor': 1"},
	} {
		for _, pair := range []struct{ dev, out string }{
			{"docker/scylla/schema.cql", "docker/scylla/" + env.suffix},
			{filepath.Join(chat, "schema.cql"), filepath.Join(chat, env.suffix)},
		} {
			t.Run(env.name+"/"+pair.out, func(t *testing.T) {
				dev := repoFile(t, pair.dev)
				got := repoFile(t, pair.out)
				rendered, names, err := Render(dev, env.cfg)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, rendered) {
					t.Fatalf("%s is stale: run `go run ./tools/scyllaschema -env %s -in %s -out %s`", pair.out, env.name, pair.dev, pair.out)
				}
				if strings.Contains(string(got), "SimpleStrategy") {
					t.Fatalf("%s still contains SimpleStrategy", pair.out)
				}
				// Every keyspace carries this environment's factor.
				if n := strings.Count(string(got), env.rf); n != len(names) {
					t.Fatalf("%s: %d of %d keyspaces carry %s", pair.out, n, len(names), env.rf)
				}
				// Everything but the keyspace statements is byte-identical.
				if a, b := stripKeyspaceLines(got), stripKeyspaceLines(dev); !bytes.Equal(a, b) {
					t.Fatalf("%s differs from %s outside the CREATE KEYSPACE statements", pair.out, pair.dev)
				}
			})
		}
	}
}

func TestPreset(t *testing.T) {
	for name, want := range map[string]Config{"dev": DevConfig, "qa": QAConfig, "prod": ProdConfig, " QA ": QAConfig} {
		got, err := Preset(name)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Preset(%q) = %+v, %v; want %+v", name, got, err, want)
		}
	}
	if _, err := Preset("staging"); err == nil || !strings.Contains(err.Error(), "dev, prod, qa") {
		t.Fatalf("unknown preset: err = %v", err)
	}
	if QAConfig.Strategy != NetworkTopologyStrategy || QAConfig.Factor != 1 {
		t.Fatalf("QA must be %s RF 1, got %+v", NetworkTopologyStrategy, QAConfig)
	}
}

func TestResolveConfig(t *testing.T) {
	none := flagOverrides{}
	cases := []struct {
		name    string
		env     string
		prod    bool
		flags   flagOverrides
		vars    map[string]string
		want    Config
		wantErr string
	}{
		{name: "nothing is the dev default", want: DevConfig},
		{name: "variables without a preset", vars: map[string]string{EnvStrategy: "NetworkTopologyStrategy", EnvFactor: "2"},
			want: Config{Strategy: NetworkTopologyStrategy, Factor: 2}},
		{name: "-env qa", env: "qa", want: QAConfig},
		{name: "-env prod", env: "prod", want: ProdConfig},
		{name: "-prod", prod: true, want: ProdConfig},
		{name: "-prod with -env prod", prod: true, env: "prod", want: ProdConfig},
		{name: "-prod with -env qa disagree", prod: true, env: "qa", wantErr: "disagree"},
		{name: "a preset beats a stray variable", env: "qa", vars: map[string]string{EnvFactor: "3"}, want: QAConfig},
		{name: "an explicit flag beats the preset", env: "qa", flags: flagOverrides{Factor: "2"},
			want: Config{Strategy: NetworkTopologyStrategy, Factor: 2}},
		{name: "datacenters on the qa preset inherit RF 1", env: "qa", flags: flagOverrides{Datacenters: "ap-south-1"},
			want: Config{Strategy: NetworkTopologyStrategy, Factor: 1, Datacenters: []Datacenter{{"ap-south-1", 1}}}},
		{name: "unknown preset", env: "staging", wantErr: "is not one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			if flags == (flagOverrides{}) {
				flags = none
			}
			got, err := resolveConfig(tc.env, tc.prod, flags, envOf(tc.vars))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("config = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The five production keyspaces named in the deployment contract are all in
// the dev schema, so one render covers them all.
func TestDevSchemaCreatesEveryProductionKeyspace(t *testing.T) {
	got := Keyspaces(repoFile(t, "docker/scylla/schema.cql"))
	want := []string{"social_feed", "social_notify", "social_engagement", "chatservice", "social_analytics"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keyspaces = %v, want %v", got, want)
	}
	if !strings.Contains(string(repoFile(t, "docker/scylla/schema.prod.cql")), "'replication_factor': 3") {
		t.Fatal("schema.prod.cql does not carry RF 3")
	}
}

func stripKeyspaceLines(b []byte) []byte {
	return keyspaceRe.ReplaceAll(b, nil)
}
