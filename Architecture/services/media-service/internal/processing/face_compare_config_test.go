package processing

import (
	"strings"
	"testing"
)

func faceEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveFaceCompareSettings_DisabledByDefault(t *testing.T) {
	s, err := ResolveFaceCompareSettings(faceEnv(map[string]string{"ENV": "prod"}))
	if err != nil || s.Enabled || s.MatchThreshold != DefaultFaceMatchThreshold {
		t.Fatalf("unset = %+v, %v; want disabled with the default threshold", s, err)
	}
}

// The mock never runs outside local/dev, whatever else is configured.
func TestResolveFaceCompareSettings_MockRefusedOutsideLocalDev(t *testing.T) {
	base := map[string]string{
		EnvFaceCompareEnabled:  "true",
		EnvFaceCompareBackend:  "mock",
		"INTERNAL_SERVICE_KEY": "k",
	}
	for _, env := range []map[string]string{
		{"ENV": ""},
		{"ENV": "prod"},
		{"ENV": "production"},
		{"ENV": "staging"},
		{"ENV": "test"},
		{"ENV": "dev", "DEPLOY_ENV": "production"},
		{"ENV": "local", "APP_ENV": "staging"},
		{"ENV": "development", "ENVIRONMENT": "prod"},
		{"ENV": "dev", "DEPLOY_ENV": "staging"},
		{"ENV": "localhost"},
		{"ENV": "dev-prod"},
		{"DEPLOY_ENV": "local"},
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range env {
			m[k] = v
		}
		if _, err := ResolveFaceCompareSettings(faceEnv(m)); err == nil || !strings.Contains(err.Error(), "mock is refused") {
			t.Fatalf("env %v: err=%v, want the mock refused", env, err)
		}
	}
	for _, e := range []string{"local", "dev", "Development"} {
		m := map[string]string{"ENV": e}
		for k, v := range base {
			m[k] = v
		}
		s, err := ResolveFaceCompareSettings(faceEnv(m))
		if err != nil || !s.Enabled || s.Backend != FaceBackendMock {
			t.Fatalf("ENV=%s: %+v, %v; want the mock allowed", e, s, err)
		}
	}
}

func TestResolveFaceCompareSettings_Refusals(t *testing.T) {
	cases := map[string]map[string]string{
		"no backend":     {EnvFaceCompareEnabled: "true", "INTERNAL_SERVICE_KEY": "k", "ENV": "dev"},
		"unknown":        {EnvFaceCompareEnabled: "true", EnvFaceCompareBackend: "azure", "INTERNAL_SERVICE_KEY": "k"},
		"no key":         {EnvFaceCompareEnabled: "true", EnvFaceCompareBackend: "mock", "ENV": "dev"},
		"bad flag":       {EnvFaceCompareEnabled: "yes"},
		"bad threshold":  {EnvFaceMatchThreshold: "101"},
		"no region":      {EnvFaceCompareEnabled: "true", EnvFaceCompareBackend: "rekognition", "INTERNAL_SERVICE_KEY": "k"},
		"static keys":    {EnvFaceCompareEnabled: "true", EnvFaceCompareBackend: "rekognition", "INTERNAL_SERVICE_KEY": "k", "AWS_REGION": "ap-south-1", "AWS_ACCESS_KEY_ID": "x"},
		"prod sans IRSA": {EnvFaceCompareEnabled: "true", EnvFaceCompareBackend: "rekognition", "INTERNAL_SERVICE_KEY": "k", "AWS_REGION": "ap-south-1", "DEPLOY_ENV": "production"},
	}
	for name, env := range cases {
		if _, err := ResolveFaceCompareSettings(faceEnv(env)); err == nil {
			t.Fatalf("%s: accepted %v", name, env)
		}
	}
	s, err := ResolveFaceCompareSettings(faceEnv(map[string]string{
		EnvFaceCompareEnabled: "true", EnvFaceCompareBackend: "rekognition", "INTERNAL_SERVICE_KEY": "k",
		"AWS_REGION": "ap-south-1", "DEPLOY_ENV": "production", "ENV": "prod",
		"AWS_ROLE_ARN": "arn:aws:iam::1:role/media", "AWS_WEB_IDENTITY_TOKEN_FILE": "/var/run/token",
		EnvFaceMatchThreshold: "92.5",
	}))
	if err != nil || s.Backend != FaceBackendRekognition || s.Region != "ap-south-1" || s.MatchThreshold != 92.5 {
		t.Fatalf("prod rekognition with IRSA = %+v, %v", s, err)
	}
}
