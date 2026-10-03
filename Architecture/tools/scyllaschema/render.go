// scyllaschema renders a Scylla schema file for a target environment.
//
// The dev schema (Architecture/docker/scylla/schema.cql, and chat-service's
// own copy of the chatservice keyspace) creates every keyspace with
// SimpleStrategy and replication_factor 1, which is right for the one-node
// compose cluster and wrong for production: Scylla on EKS runs three nodes
// in one datacenter and the keyspaces must be NetworkTopologyStrategy with
// RF 3 or a single node loss makes partitions unreadable.
//
// Rather than hand-maintain two schema files that drift, this tool rewrites
// ONLY the `WITH replication = {...}` clause of each CREATE KEYSPACE
// statement and leaves every other byte (tables, comments, line endings)
// untouched. schema.prod.cql is the committed render for production; the
// -check mode fails when it is stale.
//
// Configuration (environment, flags override):
//
//	SCYLLA_REPLICATION_STRATEGY  SimpleStrategy | NetworkTopologyStrategy   (default SimpleStrategy)
//	SCYLLA_REPLICATION_FACTOR    integer >= 1                                (default 1)
//	SCYLLA_DATACENTERS           optional, NetworkTopologyStrategy only:
//	                             "dc1" or "dc1:3,dc2:2". When empty the
//	                             clause uses 'replication_factor': N, which
//	                             Scylla applies to every datacenter it knows.
//
// Usage:
//
//	go run ./tools/scyllaschema -in docker/scylla/schema.cql -out docker/scylla/schema.prod.cql
//	SCYLLA_REPLICATION_STRATEGY=NetworkTopologyStrategy SCYLLA_REPLICATION_FACTOR=3 \
//	  go run ./tools/scyllaschema -in ... -out ... [-check]
package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Strategy names Scylla accepts. Values are matched case-insensitively and
// rendered canonically.
const (
	SimpleStrategy          = "SimpleStrategy"
	NetworkTopologyStrategy = "NetworkTopologyStrategy"
)

// Environment variable names.
const (
	EnvStrategy    = "SCYLLA_REPLICATION_STRATEGY"
	EnvFactor      = "SCYLLA_REPLICATION_FACTOR"
	EnvDatacenters = "SCYLLA_DATACENTERS"
)

// Config is one replication policy applied to every keyspace in a file.
type Config struct {
	Strategy string
	Factor   int
	// Datacenters, NetworkTopologyStrategy only. Each entry is "name" (uses
	// Factor) or "name:rf". Empty means the datacenter-agnostic form.
	Datacenters []Datacenter
}

// Datacenter is one entry of SCYLLA_DATACENTERS.
type Datacenter struct {
	Name   string
	Factor int
}

// DevConfig is today's compose cluster: what every CREATE KEYSPACE in the
// checked-in dev schema says.
var DevConfig = Config{Strategy: SimpleStrategy, Factor: 1}

// ProdConfig is the first production deployment: Scylla operator on EKS,
// three nodes, one datacenter, RF 3 (aws-fixes-contract, 3 Oct 2026).
var ProdConfig = Config{Strategy: NetworkTopologyStrategy, Factor: 3}

// ConfigFromEnv reads the three variables with dev defaults. getenv nil
// means the process environment.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DevConfig
	if v := strings.TrimSpace(getenv(EnvStrategy)); v != "" {
		cfg.Strategy = v
	}
	if v := strings.TrimSpace(getenv(EnvFactor)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %q is not an integer", EnvFactor, v)
		}
		cfg.Factor = n
	}
	if v := strings.TrimSpace(getenv(EnvDatacenters)); v != "" {
		dcs, err := ParseDatacenters(v, cfg.Factor)
		if err != nil {
			return Config{}, err
		}
		cfg.Datacenters = dcs
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ParseDatacenters parses "dc1" / "dc1:3,dc2:2". A name without ":rf" takes
// defaultFactor.
func ParseDatacenters(raw string, defaultFactor int) ([]Datacenter, error) {
	var out []Datacenter
	seen := map[string]struct{}{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, rfText, hasRF := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "'\"{}") {
			return nil, fmt.Errorf("%s: %q is not a datacenter name", EnvDatacenters, entry)
		}
		rf := defaultFactor
		if hasRF {
			n, err := strconv.Atoi(strings.TrimSpace(rfText))
			if err != nil {
				return nil, fmt.Errorf("%s: %q has a non-integer replication factor", EnvDatacenters, entry)
			}
			rf = n
		}
		if rf < 1 {
			return nil, fmt.Errorf("%s: %q must have a replication factor >= 1", EnvDatacenters, entry)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%s: datacenter %q listed twice", EnvDatacenters, name)
		}
		seen[name] = struct{}{}
		out = append(out, Datacenter{Name: name, Factor: rf})
	}
	return out, nil
}

// Validate normalises the strategy name and rejects what Scylla would.
func (c *Config) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Strategy)) {
	case strings.ToLower(SimpleStrategy):
		c.Strategy = SimpleStrategy
	case strings.ToLower(NetworkTopologyStrategy):
		c.Strategy = NetworkTopologyStrategy
	default:
		return fmt.Errorf("%s: %q is not %s or %s", EnvStrategy, c.Strategy, SimpleStrategy, NetworkTopologyStrategy)
	}
	if c.Factor < 1 {
		return fmt.Errorf("%s: %d must be >= 1", EnvFactor, c.Factor)
	}
	if c.Strategy == SimpleStrategy && len(c.Datacenters) > 0 {
		return fmt.Errorf("%s is only meaningful with %s", EnvDatacenters, NetworkTopologyStrategy)
	}
	return nil
}

// ReplicationClause builds the `{...}` map of a CREATE KEYSPACE statement.
//
//	SimpleStrategy, 1              → {'class': 'SimpleStrategy', 'replication_factor': 1}
//	NetworkTopologyStrategy, 3     → {'class': 'NetworkTopologyStrategy', 'replication_factor': 3}
//	NetworkTopologyStrategy, dc1:3 → {'class': 'NetworkTopologyStrategy', 'dc1': 3}
//
// The datacenter-agnostic NetworkTopologyStrategy form is what Scylla
// documents for "the same RF in every datacenter"; it needs no knowledge of
// the ScyllaCluster's datacenter name at render time.
func (c Config) ReplicationClause() (string, error) {
	cfg := c
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	if cfg.Strategy == SimpleStrategy || len(cfg.Datacenters) == 0 {
		return fmt.Sprintf("{'class': '%s', 'replication_factor': %d}", cfg.Strategy, cfg.Factor), nil
	}
	parts := make([]string, 0, len(cfg.Datacenters)+1)
	parts = append(parts, fmt.Sprintf("'class': '%s'", cfg.Strategy))
	dcs := append([]Datacenter(nil), cfg.Datacenters...)
	sort.Slice(dcs, func(i, j int) bool { return dcs[i].Name < dcs[j].Name })
	for _, dc := range dcs {
		parts = append(parts, fmt.Sprintf("'%s': %d", dc.Name, dc.Factor))
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}

// keyspaceRe matches the replication map of a CREATE KEYSPACE statement,
// across lines, in either `IF NOT EXISTS` form. Group 1 is everything up to
// and including `replication =` (kept verbatim), group 2 the keyspace name,
// group 3 the old map.
var keyspaceRe = regexp.MustCompile(`(?is)(CREATE\s+KEYSPACE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)\s+WITH\s+replication\s*=\s*)(\{[^}]*\})`)

// Render rewrites every CREATE KEYSPACE replication map in src to cfg and
// returns the result plus the keyspace names rewritten, in file order.
// Every other byte, including line endings, is preserved. A file with no
// CREATE KEYSPACE is an error: there is nothing to render.
func Render(src []byte, cfg Config) ([]byte, []string, error) {
	clause, err := cfg.ReplicationClause()
	if err != nil {
		return nil, nil, err
	}
	matches := keyspaceRe.FindAllSubmatchIndex(src, -1)
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("no CREATE KEYSPACE ... WITH replication = {...} statement found")
	}
	var out []byte
	var names []string
	last := 0
	for _, m := range matches {
		// m[0]:m[1] whole; m[2]:m[3] group 1; m[4]:m[5] name; m[6]:m[7] map.
		out = append(out, src[last:m[3]]...)
		out = append(out, clause...)
		last = m[7]
		names = append(names, string(src[m[4]:m[5]]))
	}
	out = append(out, src[last:]...)
	return out, names, nil
}

// Keyspaces lists the keyspace names a schema creates, in file order.
func Keyspaces(src []byte) []string {
	var names []string
	for _, m := range keyspaceRe.FindAllSubmatch(src, -1) {
		names = append(names, string(m[2]))
	}
	return names
}
