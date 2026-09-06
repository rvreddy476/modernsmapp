package service

// The value half of a variation axis, checked without a database.
//
// axisValueCode is where free text is refused, and the refusal is the single
// most consequential line in this step: on a shared catalogue a value that
// gets through is a permanent, public spelling that every seller of that item
// inherits. So it is tested here, on its own, where the test can be about
// nothing else.

import (
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

func enumDef(code string) *postgres.AttributeDefinition {
	return &postgres.AttributeDefinition{Code: code, DataType: "enum", IsVariantAxis: true}
}

func options(codes ...string) []*postgres.AttributeEnumValue {
	out := make([]*postgres.AttributeEnumValue, 0, len(codes))
	for _, c := range codes {
		out = append(out, &postgres.AttributeEnumValue{Code: c, Label: strings.ToUpper(c)})
	}
	return out
}

// TestAnAxisValueMustBeACodeNotFreeText is the refusal, stated.
//
// Every one of these is a value a well-meaning client might send, and every
// one of them would, if accepted, become a colour of its own that no filter
// could ever reunite with "blue":
//
//	"Blue"         the label, from a client that rendered the label and sent
//	               back what it rendered
//	"blue "        a trailing space from a text box
//	"Navy Blue"    the seller's own words
//	"BLUE"         a client that upper-cases for display
//
// The trimmed case is accepted — the service trims before it gets here, and
// a leading space is a keystroke rather than an opinion — but the other
// three are refused, and the message says why and lists the codes.
func TestAnAxisValueMustBeACodeNotFreeText(t *testing.T) {
	d := enumDef("colour")
	opts := options("blue", "red")

	if code, reason := axisValueCode(d, "blue", opts); reason != "" || code != "blue" {
		t.Fatalf("the option's own code was refused: %q / %q", code, reason)
	}

	for _, raw := range []string{"Blue", "BLUE", "Navy Blue", "blue-ish", "azul"} {
		code, reason := axisValueCode(d, raw, opts)
		if reason == "" {
			t.Fatalf("%q was accepted as a colour. It is not one of the option codes, so it "+
				"becomes a permanent fourth colour of this item that every seller inherits and "+
				"no filter reunites with %q.", raw, code)
		}
		// The message has to be actionable: it must name the codes, or the
		// client is left guessing at a vocabulary it cannot see.
		if !strings.Contains(reason, "blue") || !strings.Contains(reason, "red") {
			t.Errorf("the refusal of %q does not list the codes the client should send: %s", raw, reason)
		}
		if !strings.Contains(reason, "Free text is refused") {
			t.Errorf("the refusal of %q does not say that free text is the thing being refused: %s",
				raw, reason)
		}
	}
}

// TestAnAxisWithNoOptionsSaysSoRatherThanRefusingTheValue distinguishes "you
// sent the wrong code" from "there are no codes yet".
//
// They need different people: the first is the seller's client, the second is
// an operator who created a definition and never gave it values.
func TestAnAxisWithNoOptionsSaysSoRatherThanRefusingTheValue(t *testing.T) {
	_, reason := axisValueCode(enumDef("colour"), "blue", nil)
	if !strings.Contains(reason, "no options defined") {
		t.Fatalf("an axis with no enum values should say so, got: %s", reason)
	}
}

func TestAnIntegerAxisIsNormalisedNotPassedThrough(t *testing.T) {
	d := &postgres.AttributeDefinition{Code: "shoe_size", DataType: "integer", IsVariantAxis: true}

	// "007" and "7" must not become two combinations of the same size.
	for _, raw := range []string{"7", "007", "+7"} {
		code, reason := axisValueCode(d, raw, nil)
		if raw == "+7" {
			if reason == "" {
				t.Errorf("%q was accepted as a whole number", raw)
			}
			continue
		}
		if reason != "" {
			t.Fatalf("%q refused: %s", raw, reason)
		}
		if code != "7" {
			t.Fatalf("%q normalised to %q, expected \"7\" — otherwise it is a second size 7", raw, code)
		}
	}

	if _, reason := axisValueCode(d, "seven", nil); reason == "" {
		t.Error("\"seven\" was accepted on an integer axis")
	}

	min, max := 1.0, 20.0
	d.MinNum, d.MaxNum = &min, &max
	if _, reason := axisValueCode(d, "40", nil); reason == "" {
		t.Error("a value above the definition's maximum was accepted")
	}
	if _, reason := axisValueCode(d, "0", nil); reason == "" {
		t.Error("a value below the definition's minimum was accepted")
	}
}

// TestATextAxisValueCannotBreakTheCombinationKey guards the one character
// class the canonical key cannot survive.
//
// The key is `code=value` pairs joined with `|`. A value containing either
// character could make two different combinations produce one key, which
// would show up as a listing being refused a combination it has never used.
func TestATextAxisValueCannotBreakTheCombinationKey(t *testing.T) {
	d := &postgres.AttributeDefinition{Code: "finish", DataType: "text", IsVariantAxis: true}

	if code, reason := axisValueCode(d, "brushed-steel", nil); reason != "" || code != "brushed-steel" {
		t.Fatalf("an ordinary text value was refused: %q / %s", code, reason)
	}
	for _, raw := range []string{"a|b", "a=b", strings.Repeat("x", 129)} {
		if _, reason := axisValueCode(d, raw, nil); reason == "" {
			t.Errorf("%q was accepted as a text axis value; it cannot survive the combination key",
				truncate(raw))
		}
	}
	if _, reason := axisValueCode(d, "", nil); reason == "" {
		t.Error("an empty value was accepted")
	}
}

func TestATextAxisHonoursTheDefinitionsBounds(t *testing.T) {
	minLen, maxLen := 2, 6
	pattern := `^[a-z]+$`
	d := &postgres.AttributeDefinition{
		Code: "finish", DataType: "text", IsVariantAxis: true,
		MinLen: &minLen, MaxLen: &maxLen, Regex: &pattern,
	}
	if _, reason := axisValueCode(d, "a", nil); reason == "" {
		t.Error("a value shorter than min_len was accepted")
	}
	if _, reason := axisValueCode(d, "abcdefgh", nil); reason == "" {
		t.Error("a value longer than max_len was accepted")
	}
	if _, reason := axisValueCode(d, "Matte", nil); reason == "" {
		t.Error("a value that fails the definition's regex was accepted")
	}
	if code, reason := axisValueCode(d, "matte", nil); reason != "" || code != "matte" {
		t.Fatalf("a value inside every bound was refused: %q / %s", code, reason)
	}
}

// TestANonDiscreteAttributeCannotKeyAVariant covers the fall-through.
//
// 025 already CHECKs that only enum/text/integer may be marked as an axis, so
// this is unreachable through a well-formed definition. It is tested because
// the alternative to a named refusal is a silent empty value code.
func TestANonDiscreteAttributeCannotKeyAVariant(t *testing.T) {
	d := &postgres.AttributeDefinition{Code: "item_weight", DataType: "measure"}
	code, reason := axisValueCode(d, "250", nil)
	if reason == "" || code != "" {
		t.Fatalf("a measure was accepted as a variant axis value: %q / %q", code, reason)
	}
}

func truncate(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}
