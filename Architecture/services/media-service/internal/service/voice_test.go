package service

import (
	"testing"

	"github.com/atpost/media-service/internal/processing"
)

// P0-6 acceptance: reject unsupported / spoofed / oversized / >180 s audio.

func TestValidateUploadMIME_Audio(t *testing.T) {
	allowed := []string{"audio/mp4", "audio/mpeg", "audio/ogg", "audio/wav", "audio/webm", "audio/amr", "audio/opus"}
	for _, ct := range allowed {
		if err := ValidateUploadMIME(ct, "audio"); err != nil {
			t.Errorf("%s should be allowed for audio: %v", ct, err)
		}
	}
	rejected := []string{"application/octet-stream", "text/plain", "video/mp4", "image/png", "application/x-msdownload", ""}
	for _, ct := range rejected {
		if err := ValidateUploadMIME(ct, "audio"); err == nil {
			t.Errorf("%s must be rejected for audio", ct)
		}
	}
	// Parameters after ';' must not defeat the allow-list.
	if err := ValidateUploadMIME("audio/mp4; codecs=mp4a.40.2", "audio"); err != nil {
		t.Errorf("parameterized MIME should be accepted: %v", err)
	}
}

func TestValidateVoiceDuration(t *testing.T) {
	cases := []struct {
		name    string
		seconds float64
		wantErr bool
	}{
		{"unmeasured is rejected", 0, true},
		{"negative is rejected", -5, true},
		{"too short", 0.4, true},
		{"minimum ok", 1, false},
		{"typical", 42.5, false},
		{"exactly at cap", 180, false},
		{"just over cap", 180.5, true},
		{"way over cap", 3600, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateVoiceDuration(tc.seconds)
			if tc.wantErr && err == nil {
				t.Fatalf("expected rejection for %.1fs", tc.seconds)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection for %.1fs: %v", tc.seconds, err)
			}
		})
	}
}

func TestValidateUpload_AudioSize(t *testing.T) {
	if err := ValidateUpload("audio", "general", "audio/mp4", MaxVoiceSizeBytes+1); err == nil {
		t.Fatal("oversized audio must be rejected")
	}
	if err := ValidateUpload("audio", "general", "audio/mp4", 1024*1024); err != nil {
		t.Fatalf("1 MB audio should be accepted: %v", err)
	}
}

func TestValidateAudioMagicBytes(t *testing.T) {
	valid := map[string][]byte{
		"m4a":  append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A ")...),
		"ogg":  []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00"),
		"wav":  append([]byte("RIFF\x24\x08\x00\x00"), []byte("WAVEfmt ")...),
		"flac": []byte("fLaC\x00\x00\x00\x22\x12\x00\x12\x00"),
		"mp3":  []byte("ID3\x03\x00\x00\x00\x00\x00\x00\x00\x00"),
		"amr":  []byte("#!AMR\n\x3c\x00\x00\x00\x00\x00"),
	}
	for name, data := range valid {
		if _, ok := processing.ValidateAudioMagicBytes(data); !ok {
			t.Errorf("%s header should validate", name)
		}
	}

	spoofed := map[string][]byte{
		"windows exe":  []byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00"),
		"elf binary":   []byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00"),
		"shell script": []byte("#!/bin/sh\necho hi\n"),
		"png":          []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d"),
		"too short":    []byte("OggS"),
	}
	for name, data := range spoofed {
		if _, ok := processing.ValidateAudioMagicBytes(data); ok {
			t.Errorf("%s must NOT validate as audio", name)
		}
	}
}
