package http

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
)

func TestResolvePhotoSafetyConfig(t *testing.T) {
	cfg, err := ResolvePhotoSafetyConfig(envOf(nil))
	if err != nil || !reflect.DeepEqual(cfg, service.DefaultPhotoSafetyConfig()) || cfg.MaxPhotos != 6 ||
		cfg.ExplicitMinConfidence != 80 || cfg.ReviewMinConfidence != 80 || !cfg.RequireFaceOnPrimary ||
		!cfg.RecheckEnabled || cfg.RecheckInterval != 24*time.Hour {
		t.Fatalf("unset = %+v, %v", cfg, err)
	}
	cfg, err = ResolvePhotoSafetyConfig(envOf(map[string]string{
		"DATING_PHOTO_MAX_PER_PROFILE": "9", "DATING_PHOTO_EXPLICIT_LABELS": " Explicit Nudity , Explicit ",
		"DATING_PHOTO_EXPLICIT_MIN_CONFIDENCE": "90", "DATING_PHOTO_REVIEW_LABELS": "Suggestive",
		"DATING_PHOTO_REVIEW_MIN_CONFIDENCE": "70.5", "DATING_PHOTO_REQUIRE_FACE": "false",
		"DATING_PHOTO_RECHECK_ENABLED": "false", "DATING_PHOTO_RECHECK_INTERVAL_HOURS": "6",
	}))
	if err != nil || cfg.MaxPhotos != 9 || !reflect.DeepEqual(cfg.ExplicitLabels, []string{"Explicit Nudity", "Explicit"}) ||
		cfg.ExplicitMinConfidence != 90 || !reflect.DeepEqual(cfg.ReviewLabels, []string{"Suggestive"}) ||
		cfg.ReviewMinConfidence != 70.5 || cfg.RequireFaceOnPrimary || cfg.RecheckEnabled || cfg.RecheckInterval != 6*time.Hour {
		t.Fatalf("set = %+v, %v", cfg, err)
	}
	for name, env := range map[string]map[string]string{
		"max zero":           {"DATING_PHOTO_MAX_PER_PROFILE": "0"},
		"max too many":       {"DATING_PHOTO_MAX_PER_PROFILE": "13"},
		"confidence too low": {"DATING_PHOTO_EXPLICIT_MIN_CONFIDENCE": "40"},
		"confidence not num": {"DATING_PHOTO_REVIEW_MIN_CONFIDENCE": "high"},
		"labels only commas": {"DATING_PHOTO_REVIEW_LABELS": " , ,"},
		"bool garbage":       {"DATING_PHOTO_REQUIRE_FACE": "maybe"},
		"interval zero":      {"DATING_PHOTO_RECHECK_INTERVAL_HOURS": "0"},
		"interval too long":  {"DATING_PHOTO_RECHECK_INTERVAL_HOURS": "169"},
	} {
		if _, err := ResolvePhotoSafetyConfig(envOf(env)); err == nil || !strings.Contains(err.Error(), "DATING_PHOTO_") {
			t.Fatalf("%s: err=%v, want a DATING_PHOTO_* error", name, err)
		}
	}
}
