package blob

import (
	"strings"
	"testing"
)

// checkS3Credentials decides whether NewS3IRSA may build an S3 client. The
// instance-role opt-in exists for the single-server dev stack on EC2
// (infra/devbox); the rows that matter most are the ones proving a
// production or QA pod can never use it.
func TestCheckS3Credentials(t *testing.T) {
	irsa := map[string]string{
		"AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
		"AWS_ROLE_ARN":                "arn:aws:iam::000000000000:role/media-service",
	}
	with := func(base map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string // substring; "" means allowed
	}{
		{"irsa in production", with(irsa, map[string]string{"ENV": "prod"}), ""},
		{"irsa without ENV", irsa, ""},
		{"static access key refused", with(irsa, map[string]string{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE"}), "static AWS credentials are forbidden"},
		{"static secret refused even with the opt-in", map[string]string{"ENV": "local", EnvAllowInstanceRole: "true", "AWS_SECRET_ACCESS_KEY": "x"}, "static AWS credentials are forbidden"},
		{"no identity, no opt-in", map[string]string{"ENV": "local"}, "node-role fallback is forbidden"},
		{"half an identity is no identity", map[string]string{"ENV": "local", "AWS_ROLE_ARN": "arn:aws:iam::0:role/x"}, "node-role fallback is forbidden"},
		{"opt-in on local", map[string]string{"ENV": "local", EnvAllowInstanceRole: "true"}, ""},
		{"opt-in on dev, any case", map[string]string{"ENV": " Dev ", EnvAllowInstanceRole: "TRUE"}, ""},
		{"opt-in needs the value true", map[string]string{"ENV": "local", EnvAllowInstanceRole: "1"}, "node-role fallback is forbidden"},
		{"opt-in refused in production", map[string]string{"ENV": "prod", EnvAllowInstanceRole: "true"}, "node-role fallback is forbidden"},
		{"opt-in refused with a blank ENV", map[string]string{EnvAllowInstanceRole: "true"}, "node-role fallback is forbidden"},
		{"opt-in refused when DEPLOY_ENV says production", map[string]string{"ENV": "local", "DEPLOY_ENV": "production", EnvAllowInstanceRole: "true"}, "node-role fallback is forbidden"},
		{"opt-in refused when APP_ENV says qa", map[string]string{"ENV": "dev", "APP_ENV": "qa", EnvAllowInstanceRole: "true"}, "node-role fallback is forbidden"},
		{"opt-in with matching APP_ENV", map[string]string{"ENV": "dev", "APP_ENV": "local", EnvAllowInstanceRole: "true"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkS3Credentials(func(k string) string { return tc.env[k] })
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
