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
		prod     = flag.Bool("prod", false, "shorthand for the production policy: "+NetworkTopologyStrategy+" RF 3")
	)
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: scyllaschema -in schema.cql -out schema.prod.cql [-prod] [-check]")
		os.Exit(2)
	}

	getenv := func(k string) string {
		switch {
		case k == EnvStrategy && *strategy != "":
			return *strategy
		case k == EnvFactor && *factor != "":
			return *factor
		case k == EnvDatacenters && *dcs != "":
			return *dcs
		}
		if *prod {
			switch k {
			case EnvStrategy:
				return ProdConfig.Strategy
			case EnvFactor:
				return fmt.Sprint(ProdConfig.Factor)
			}
		}
		return os.Getenv(k)
	}
	cfg, err := ConfigFromEnv(getenv)
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
