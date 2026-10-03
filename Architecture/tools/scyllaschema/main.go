package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	var (
		in       = flag.String("in", "", "source schema (dev) to render from")
		out      = flag.String("out", "", "rendered schema to write; '-' for stdout")
		check    = flag.Bool("check", false, "do not write; exit 1 when -out differs from the render")
		strategy = flag.String("strategy", "", "overrides "+EnvStrategy)
		factor   = flag.String("factor", "", "overrides "+EnvFactor)
		dcs      = flag.String("datacenters", "", "overrides "+EnvDatacenters)
		prod     = flag.Bool("prod", false, "shorthand for -env prod: "+NetworkTopologyStrategy+" RF 3")
		envName  = flag.String("env", "", "preset policy: dev ("+SimpleStrategy+" RF 1), qa ("+NetworkTopologyStrategy+" RF 1), prod ("+NetworkTopologyStrategy+" RF 3)")
	)
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: scyllaschema -in schema.cql -out schema.<env>.cql [-env dev|qa|prod | -prod] [-check]")
		os.Exit(2)
	}

	cfg, err := resolveConfig(*envName, *prod, flagOverrides{Strategy: *strategy, Factor: *factor, Datacenters: *dcs}, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scyllaschema:", err)
		os.Exit(2)
	}

	src, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scyllaschema:", err)
		os.Exit(2)
	}
	rendered, names, err := Render(src, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scyllaschema:", err)
		os.Exit(2)
	}
	clause, _ := cfg.ReplicationClause()

	if *check {
		current, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scyllaschema: %s is missing or unreadable: %v\n", *out, err)
			os.Exit(1)
		}
		if !bytes.Equal(current, rendered) {
			fmt.Fprintf(os.Stderr, "scyllaschema: %s is stale; re-run without -check to regenerate it from %s\n", *out, *in)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "scyllaschema: %s is up to date (%s on %s)\n", *out, clause, strings.Join(names, ", "))
		return
	}

	if *out == "-" {
		_, _ = os.Stdout.Write(rendered)
		return
	}
	if err := os.WriteFile(*out, rendered, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "scyllaschema:", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "scyllaschema: wrote %s with %s on %s\n", *out, clause, strings.Join(names, ", "))
}

// flagOverrides are the explicit -strategy / -factor / -datacenters values.
type flagOverrides struct {
	Strategy, Factor, Datacenters string
}

// resolveConfig picks the replication policy. Precedence, per setting: an
// explicit flag, then the -env preset (or -prod), then the SCYLLA_*
// environment variable, then the dev default. -prod is shorthand for
// -env prod; asking for both with different presets is an error.
func resolveConfig(envName string, prod bool, flags flagOverrides, getenv func(string) string) (Config, error) {
	if prod {
		if envName != "" && !strings.EqualFold(strings.TrimSpace(envName), "prod") {
			return Config{}, fmt.Errorf("-prod and -env %q disagree", envName)
		}
		envName = "prod"
	}
	var preset *Config
	if envName != "" {
		p, err := Preset(envName)
		if err != nil {
			return Config{}, err
		}
		preset = &p
	}
	return ConfigFromEnv(func(k string) string {
		switch {
		case k == EnvStrategy && flags.Strategy != "":
			return flags.Strategy
		case k == EnvFactor && flags.Factor != "":
			return flags.Factor
		case k == EnvDatacenters && flags.Datacenters != "":
			return flags.Datacenters
		}
		if preset != nil {
			switch k {
			case EnvStrategy:
				return preset.Strategy
			case EnvFactor:
				return fmt.Sprint(preset.Factor)
			}
		}
		return getenv(k)
	})
}
