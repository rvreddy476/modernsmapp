package service

import "testing"

func TestNormalizeReportInputLaunchCompatibility(t *testing.T) {
	t.Parallel()
	cases := []struct {
		entity, reason         string
		wantEntity, wantReason string
	}{
		{"post", "spam", "post", "spam"},
		{"reel", "hate_speech", "post", "hate_abuse"},
		{"video", "violence", "post", "violence_threat"},
		{" POST ", " NUDITY ", "post", "sexual_content"},
	}
	for _, tc := range cases {
		gotEntity, gotReason := NormalizeReportInput(tc.entity, tc.reason)
		if gotEntity != tc.wantEntity || gotReason != tc.wantReason {
			t.Fatalf("NormalizeReportInput(%q,%q)=(%q,%q), want (%q,%q)",
				tc.entity, tc.reason, gotEntity, gotReason, tc.wantEntity, tc.wantReason)
		}
	}
}
