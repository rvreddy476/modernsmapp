package handle

import (
	"testing"
)

func TestValidate_Valid(t *testing.T) {
	valid := []string{
		"abc",
		"hello_world",
		"user123",
		"a1b2c3",
		"my_channel_name",
		"abc",                      // min length
		"abcdefghijklmnopqrstuvwx", // 24 chars, max length
	}
	for _, h := range valid {
		if err := Validate(h); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", h, err)
		}
	}
}

func TestValidate_TooShort(t *testing.T) {
	if err := Validate("ab"); err != ErrTooShort {
		t.Errorf("Validate(%q) = %v, want ErrTooShort", "ab", err)
	}
}

func TestValidate_TooLong(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxy" // 25 chars
	if err := Validate(long); err != ErrTooLong {
		t.Errorf("Validate(%q) = %v, want ErrTooLong", long, err)
	}
}

func TestValidate_Uppercase(t *testing.T) {
	if err := Validate("Hello"); err != ErrInvalidChars {
		t.Errorf("Validate(%q) = %v, want ErrInvalidChars", "Hello", err)
	}
}

func TestValidate_StartsUnderscore(t *testing.T) {
	if err := Validate("_hello"); err != ErrStartsUnderscore {
		t.Errorf("Validate(%q) = %v, want ErrStartsUnderscore", "_hello", err)
	}
}

func TestValidate_EndsUnderscore(t *testing.T) {
	if err := Validate("hello_"); err != ErrEndsUnderscore {
		t.Errorf("Validate(%q) = %v, want ErrEndsUnderscore", "hello_", err)
	}
}

func TestValidate_DoubleUnderscore(t *testing.T) {
	if err := Validate("hello__world"); err != ErrDoubleUnderscore {
		t.Errorf("Validate(%q) = %v, want ErrDoubleUnderscore", "hello__world", err)
	}
}

func TestValidate_BannedWord(t *testing.T) {
	banned := []string{"admin", "atpost_user", "my_support", "official123"}
	for _, h := range banned {
		if err := Validate(h); err != ErrBannedWord {
			t.Errorf("Validate(%q) = %v, want ErrBannedWord", h, err)
		}
	}
}

func TestGenerate_FromDisplayName(t *testing.T) {
	h := Generate("John Doe")
	if err := Validate(h); err != nil {
		t.Errorf("Generate(%q) produced invalid handle %q: %v", "John Doe", h, err)
	}
	if len(h) < MinLength || len(h) > MaxLength {
		t.Errorf("Generate(%q) = %q, length %d out of range [%d, %d]", "John Doe", h, len(h), MinLength, MaxLength)
	}
}

func TestGenerate_EmptyDisplayName(t *testing.T) {
	h := Generate("")
	if err := Validate(h); err != nil {
		t.Errorf("Generate(%q) produced invalid handle %q: %v", "", h, err)
	}
	// Should start with "creator_"
	if len(h) < len("creator_") {
		t.Errorf("Generate(%q) = %q, expected to start with 'creator_'", "", h)
	}
}

func TestGenerate_SpecialChars(t *testing.T) {
	h := Generate("  @#$Raghu!!  ")
	if err := Validate(h); err != nil {
		t.Errorf("Generate(%q) produced invalid handle %q: %v", "@#$Raghu!!", h, err)
	}
}

func TestGenerate_UniquePerCall(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		h := Generate("TestUser")
		if seen[h] {
			t.Fatalf("Generate produced duplicate handle %q on iteration %d", h, i)
		}
		seen[h] = true
	}
}

func TestGenerate_LongDisplayName(t *testing.T) {
	long := "This Is A Very Long Display Name That Should Be Truncated"
	h := Generate(long)
	if err := Validate(h); err != nil {
		t.Errorf("Generate(%q) produced invalid handle %q: %v", long, h, err)
	}
	if len(h) > MaxLength {
		t.Errorf("Generate(%q) = %q, length %d exceeds max %d", long, h, len(h), MaxLength)
	}
}

func TestIsBanned(t *testing.T) {
	if !IsBanned("admin") {
		t.Error("IsBanned(admin) = false, want true")
	}
	if !IsBanned("my_atpost_channel") {
		t.Error("IsBanned(my_atpost_channel) = false, want true")
	}
	if IsBanned("johndoe123") {
		t.Error("IsBanned(johndoe123) = true, want false")
	}
}
