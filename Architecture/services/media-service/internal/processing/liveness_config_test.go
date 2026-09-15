package processing

import (
	"strings"
	"testing"
)

func TestResolveFaceCompareSettings_LivenessDefaultsAndOverrides(t *testing.T) {
	s, err := ResolveFaceCompareSettings(faceEnv(nil))
	if err != nil || s.Liveness != DefaultLivenessConfig() {
		t.Fatalf("defaults = %+v, %v", s.Liveness, err)
	}
	d := DefaultLivenessConfig()
	if d.SampleFPS != 8 || d.MaxFrames != 40 || d.MaxDurationMs != 4000 || d.EyesOpenConfidence != 80 ||
		d.RequiredBlinks != 2 || d.MinClosedFrames != 1 || d.FaceConsistency != 90 {
		t.Fatalf("documented defaults drifted: %+v", d)
	}
	s, err = ResolveFaceCompareSettings(faceEnv(map[string]string{
		EnvLivenessSampleFPS: "10", EnvLivenessMaxFrames: "30", EnvLivenessMaxDurationMs: "3500",
		EnvLivenessEyesConfidence: "85", EnvLivenessRequiredBlinks: "3", EnvLivenessMinFrames: "10",
		EnvLivenessFaceConsistency: "92.5",
	}))
	if err != nil || s.Liveness.SampleFPS != 10 || s.Liveness.MaxFrames != 30 || s.Liveness.MaxDurationMs != 3500 ||
		s.Liveness.EyesOpenConfidence != 85 || s.Liveness.RequiredBlinks != 3 || s.Liveness.MinConfidentFrames != 10 ||
		s.Liveness.FaceConsistency != 92.5 {
		t.Fatalf("overrides = %+v, %v", s.Liveness, err)
	}
	for key, raw := range map[string]string{
		EnvLivenessSampleFPS:       "0",
		EnvLivenessMaxFrames:       "100",
		EnvLivenessMaxDurationMs:   "60000",
		EnvLivenessEyesConfidence:  "20",
		EnvLivenessRequiredBlinks:  "0",
		EnvLivenessFaceConsistency: "abc",
	} {
		if _, err := ResolveFaceCompareSettings(faceEnv(map[string]string{key: raw})); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s=%s: err=%v", key, raw, err)
		}
	}
	if _, err := ResolveFaceCompareSettings(faceEnv(map[string]string{EnvLivenessMaxFrames: "8", EnvLivenessMinFrames: "9"})); err == nil {
		t.Fatalf("min frames above max frames accepted")
	}
}
