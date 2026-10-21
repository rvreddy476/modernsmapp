package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.PasswordRules.PasswordError
import org.junit.Test

/**
 * The client mirror of `validatePassword` (auth-service auth.go). The rules
 * and their ORDER are the server's; a drift here tells someone a password
 * is fine that the server then refuses, or vice versa.
 */
class PasswordRulesTest {

    @Test
    fun `a password that meets every rule passes`() {
        assertThat(PasswordRules.validate("Str0ng!pass")).isNull()
        assertThat(PasswordRules.validate("Abcdefg1?")).isNull()
    }

    @Test
    fun `rules are reported in the server's order`() {
        assertThat(PasswordRules.validate("Ab1!")).isEqualTo(PasswordError.TooShort)
        assertThat(PasswordRules.validate("abcdefg1!")).isEqualTo(PasswordError.NeedsUppercase)
        assertThat(PasswordRules.validate("Abcdefgh!")).isEqualTo(PasswordError.NeedsDigit)
        assertThat(PasswordRules.validate("Abcdefgh1")).isEqualTo(PasswordError.NeedsSpecial)
    }

    @Test
    fun `eight characters is long enough`() {
        assertThat(PasswordRules.validate("Abcde1!x")).isNull()
        assertThat(PasswordRules.validate("Abcd1!x")).isEqualTo(PasswordError.TooShort)
    }

    @Test
    fun `every character of the server's special set counts, and nothing else does`() {
        PasswordRules.SPECIAL_CHARACTERS.forEach { special ->
            assertThat(PasswordRules.validate("Abcdefg1$special")).isNull()
        }
        // A non-ASCII symbol is not in the Go regexp's class.
        assertThat(PasswordRules.validate("Abcdefg1€")).isEqualTo(PasswordError.NeedsSpecial)
        assertThat(PasswordRules.validate("Abcdefg1 ")).isEqualTo(PasswordError.NeedsSpecial)
    }

    @Test
    fun `only A to Z is an upper-case letter, as in the server's regexp`() {
        assertThat(PasswordRules.validate("éabcdefg1!")).isEqualTo(PasswordError.NeedsUppercase)
        assertThat(PasswordRules.validate("Éabcdefg1!")).isEqualTo(PasswordError.NeedsUppercase)
    }

    @Test
    fun `every error has a sentence`() {
        PasswordError.entries.forEach { error ->
            assertThat(PasswordRules.text(error)).isNotEmpty()
        }
        assertThat(PasswordRules.RULES_TEXT).contains("${PasswordRules.MIN_LENGTH}")
    }
}
