package http

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/atpost/post-service/internal/service"
)

// canonicalCreateRequest turns an accepted create-post request into stable
// bytes for the idempotency fingerprint — Slice C, C-P0-5.
//
// # WHY THE WHOLE REQUEST, AND NOT A FIELD LIST
//
// The previous fingerprint hashed six fields: text, visibility, content type,
// post type, language and media ids. Everything else the route accepts was
// invisible to it, so the same actor could reuse one `Idempotency-Key` with a
// materially different body — a different poll, different location, comments
// disabled, a different distribution policy, a different audio track — and the
// server would replay the ORIGINAL post and report success. The second request
// silently did nothing, and the client was told it worked.
//
// The audio attachment is the sharpest case: it happens AFTER the idempotent
// transaction (`handler.go`, `AttachAudioToPost`), so it is a genuine side
// effect of the request. A fingerprint that ignores it lets a retry that adds
// or changes audio be swallowed as a replay.
//
// So the fingerprint is taken over the ENTIRE bound request. Marshalling the
// struct is not laziness — it is the only version of this that cannot rot. An
// enumerated field list has to be updated by whoever adds the forty-first
// field, and the failure mode when they forget is silent and remote from their
// change. Marshalling includes new fields automatically, by construction.
//
// # WHY IT IS SAFE TO MARSHAL
//
// `encoding/json` emits struct fields in declaration order, so the output is
// deterministic for a given binary. The only non-deterministic members are the
// two `json.RawMessage` fields, which carry the client's bytes verbatim:
// `{"a":1}` and `{ "a" : 1 }` are the same document but different bytes, and
// would fingerprint differently. Both are compacted first so semantically
// identical policies hash identically.
//
// This is deliberately NOT a semantic canonicaliser — it does not reorder
// object keys inside `rich_text`. A client that reorders its own JSON keys
// between retries gets a 409 rather than a replay. That is the safe direction:
// refusing a retry costs one extra tap, while replaying a request that differs
// in ways we did not compare silently discards what the user actually asked for.
func canonicalCreateRequest(req CreatePostRequest) ([]byte, error) {
	normalised := req

	// Compact the raw-JSON members so whitespace differences do not read as
	// content differences.
	compacted, err := compactRaw(req.RichText)
	if err != nil {
		return nil, fmt.Errorf("canonicalise rich_text: %w", err)
	}
	normalised.RichText = compacted

	compacted, err = compactRaw(req.Distribution)
	if err != nil {
		return nil, fmt.Errorf("canonicalise distribution: %w", err)
	}
	normalised.Distribution = compacted

	return json.Marshal(normalised)
}

// compactRaw removes insignificant whitespace, leaving nil and empty alone.
func compactRaw(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		// nil and "" are both "absent". Normalising to nil means a client that
		// sends `""` and one that omits the field agree.
		return nil, nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return json.RawMessage(buf.Bytes()), nil
}

// createFingerprint is the value stored alongside the idempotency claim.
func createFingerprint(req CreatePostRequest) (string, error) {
	canonical, err := canonicalCreateRequest(req)
	if err != nil {
		return "", err
	}
	return service.FingerprintOf(canonical), nil
}
