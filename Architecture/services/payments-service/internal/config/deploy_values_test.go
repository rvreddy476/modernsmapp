package config

// com-2 — the owed-refund switches are DEPLOYED on, and the alarm that says
// money owed back is waiting is identical in prod and QA. Dependency-free (no
// YAML library), like suggestion-service's deployconfig_test: the env block is
// a flat map and the rule list is compared as text.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const paymentsDeployRoot = "../../../../../deploy/services/payments-service"

// deployedEnv reads the top-level env: block of a values file.
func deployedEnv(t *testing.T, file string) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(paymentsDeployRoot, file))
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer f.Close()
	out := map[string]string{}
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			in = trimmed == "env:"
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `'"`)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// ruleBlock is the text from the top-level prometheusRule: key to the next
// top-level key (or the end of the file).
func ruleBlock(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(paymentsDeployRoot, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var out []string
	in := false
	for _, line := range lines {
		top := line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#")
		if top {
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

func TestDeployedOwedRefundSwitchesAreOn(t *testing.T) {
	for _, file := range []string{"values-prod.yaml", "values-qa.yaml", "values-staging.yaml"} {
		env := deployedEnv(t, file)
		for _, k := range []string{"PAYMENTS_REFUND_REQUIRED_WORKER", "PAYMENTS_DUPLICATE_CAPTURE_CONSUMER"} {
			if env[k] != "true" {
				t.Errorf("%s: %s = %q, want 'true' (com-2: owed captures are never refunded otherwise)", file, k, env[k])
			}
		}
		if v, ok := env["PAYMENTS_ALLOW_OWED_REFUNDS_OFF"]; ok {
			t.Errorf("%s: PAYMENTS_ALLOW_OWED_REFUNDS_OFF=%q is deployed; it is an incident override, never a values setting", file, v)
		}
		// And the deployed env boots with them: prod semantics, worker on.
		if env["ENV"] == "prod" {
			cfg, err := Resolve(envMap(map[string]string{"ENV": "prod", "SERVICE_CALLERS": "commerce-service",
				"RAZORPAY_KEY_ID": "k", "RAZORPAY_KEY_SECRET": "s", "RAZORPAY_WEBHOOK_SECRET": "w",
				"PAYMENTS_REFUND_REQUIRED_WORKER":     env["PAYMENTS_REFUND_REQUIRED_WORKER"],
				"PAYMENTS_DUPLICATE_CAPTURE_CONSUMER": env["PAYMENTS_DUPLICATE_CAPTURE_CONSUMER"]}))
			if err != nil || !cfg.RefundRequiredWorker || !cfg.DuplicateCaptureConsumer {
				t.Errorf("%s: the deployed switches resolve to worker=%v consumer=%v err=%v", file,
					cfg.RefundRequiredWorker, cfg.DuplicateCaptureConsumer, err)
			}
		}
	}
}

// The payments alert list is one list in prod and QA, it names the owed-capture
// alarms, and every metric it reads is one internal/obs exports.
func TestDeployedPaymentsAlertRules(t *testing.T) {
	prod, qa := ruleBlock(t, "values-prod.yaml"), ruleBlock(t, "values-qa.yaml")
	if prod == "" {
		t.Fatal("values-prod.yaml has no prometheusRule block")
	}
	if prod != qa {
		t.Fatal("prometheusRule differs between values-prod.yaml and values-qa.yaml; keep the two lists identical")
	}
	if !strings.Contains(prod, "enabled: true") {
		t.Error("prometheusRule is not enabled")
	}
	for _, a := range []string{"PaymentsOwedCaptureNotRefunded", "PaymentsOwedCaptureParked",
		"PaymentsOwedRefundFailures", "PaymentsRefundNeedsAttention"} {
		if !strings.Contains(prod, "- alert: "+a+"\n") {
			t.Errorf("alert %s is missing", a)
		}
	}
	src, err := os.ReadFile("../obs/metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	read := regexp.MustCompile(`payments_[a-z_]+`).FindAllString(prod, -1)
	if len(read) == 0 {
		t.Fatal("no rule reads a payments_ metric")
	}
	for _, m := range read {
		if !strings.Contains(string(src), `"`+m+`"`) {
			t.Errorf("a rule reads %s but internal/obs/metrics.go exports no metric of that name", m)
		}
	}
}
