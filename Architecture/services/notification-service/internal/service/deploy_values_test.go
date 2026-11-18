package service

// lv-21 — notification-service's alert list is one list in prod and QA, it
// names the fan-out and dead-letter alarms, and every metric an expression
// reads is one this service's code registers. Dependency-free, like
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
	notificationDeployRoot = "../../../../../deploy/services/notification-service"
	notificationSourceRoot = "../.."
)

func deployedRuleBlock(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(notificationDeployRoot, file))
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

func registeredMetricSource(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(notificationSourceRoot, func(p string, d fs.DirEntry, err error) error {
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
	return sb.String()
}

func TestDeployedNotificationAlertRules(t *testing.T) {
	prod, qa := deployedRuleBlock(t, "values-prod.yaml"), deployedRuleBlock(t, "values-qa.yaml")
	if prod == "" {
		t.Fatal("values-prod.yaml has no prometheusRule block")
	}
	if prod != qa {
		t.Fatal("prometheusRule differs between values-prod.yaml and values-qa.yaml; keep the two lists identical")
	}
	if !strings.Contains(prod, "\n  enabled: true\n") {
		t.Error("prometheusRule is not enabled")
	}
	for _, a := range []string{"NotificationFanoutJobStuck", "NotificationFanoutJobsFailing",
		"NotificationFanoutTriggersLost", "NotificationEventsDeadLettered"} {
		if !strings.Contains(prod, "- alert: "+a+"\n") {
			t.Errorf("alert %s is missing", a)
		}
	}

	src := registeredMetricSource(t)
	read := regexp.MustCompile(`\batpost_notification_service_[a-z0-9_]+`).FindAllString(prod, -1)
	if len(read) == 0 {
		t.Fatal("no rule reads a notification-service metric")
	}
	for _, full := range read {
		name := full
		for _, suf := range []string{"_bucket", "_count", "_sum"} {
			name = strings.TrimSuffix(name, suf)
		}
		short := strings.TrimPrefix(name, "atpost_notification_service_")
		re := regexp.MustCompile(`Name:\s*"` + regexp.QuoteMeta(short) + `"`)
		if !re.MatchString(src) {
			t.Errorf("a rule reads %s but no notification-service code registers %q", full, short)
		}
	}
}
