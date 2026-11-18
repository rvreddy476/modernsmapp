package postschedule

// lv-21 — post-service's alert list is one list in prod and QA, it names the
// schedule, pending-video, outbox and dead-letter alarms, and every metric an
// expression reads is one this service's code (or the shared Kafka consumer
// metrics it records under its own name) registers. Dependency-free, like
// payments-service's deploy_values_test: the rule list is compared as text.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	postDeployRoot = "../../../../../deploy/services/post-service"
	postSourceRoot = "../.."
	sharedKafka    = "../../../../shared/o11y/metrics/kafka.go"
)

func ruleBlock(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(postDeployRoot, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	var out []string
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			if in {
				break
			}
			in = line == "prometheusRule:"
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// registeredNames is every metric Name in the service's non-test Go code and
// in the shared Kafka consumer metrics.
func registeredNames(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(postSourceRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sb.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sharedKafka)
	if err != nil {
		t.Fatal(err)
	}
	sb.Write(b)
	return sb.String()
}

func TestDeployedPostServiceAlertRules(t *testing.T) {
	prod, qa := ruleBlock(t, "values-prod.yaml"), ruleBlock(t, "values-qa.yaml")
	if prod == "" {
		t.Fatal("values-prod.yaml has no prometheusRule block")
	}
	if prod != qa {
		t.Fatal("prometheusRule differs between values-prod.yaml and values-qa.yaml; keep the two lists identical")
	}
	if !strings.Contains(prod, "\n  enabled: true\n") {
		t.Error("prometheusRule is not enabled")
	}
	for _, a := range []string{"PostScheduledPublishOverdue", "PostVideoPendingTooLong", "PostOutboxRelayStuck", "PostEventsDeadLettered"} {
		if !strings.Contains(prod, "- alert: "+a+"\n") {
			t.Errorf("alert %s is missing", a)
		}
	}

	src := registeredNames(t)
	read := regexp.MustCompile(`\b(atpost_post_service_[a-z0-9_]+|post_video_gate_[a-z0-9_]+)`).FindAllString(prod, -1)
	if len(read) == 0 {
		t.Fatal("no rule reads a post-service metric")
	}
	for _, full := range read {
		name := full
		for _, suf := range []string{"_bucket", "_count", "_sum"} {
			name = strings.TrimSuffix(name, suf)
		}
		short := strings.TrimPrefix(name, "atpost_post_service_")
		re := regexp.MustCompile(`Name:\s*"` + regexp.QuoteMeta(short) + `"`)
		if !re.MatchString(src) {
			t.Errorf("a rule reads %s but no post-service code (or shared/o11y/metrics/kafka.go) registers %q", full, short)
		}
	}
}
