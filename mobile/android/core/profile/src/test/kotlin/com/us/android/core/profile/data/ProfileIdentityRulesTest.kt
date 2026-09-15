package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.profile.data.ProfileIdentityRules.DobError
import com.us.android.core.profile.data.ProfileIdentityRules.FirstNameError
import org.junit.Test
import java.time.Instant
import java.time.LocalDate

/**
 * Pins the client mirror of profile-service's identity_fields.go. Every case
 * here is one the server decides the same way; a drift is a 422 in production.
 */
class ProfileIdentityRulesTest {

    // ── First name ──────────────────────────────────────────────────────

    @Test
    fun `a 50 character first name is accepted`() {
        assertThat(ProfileIdentityRules.validateFirstName("a".repeat(50))).isNull()
    }

    @Test
    fun `a 51 character first name is refused`() {
        assertThat(ProfileIdentityRules.validateFirstName("a".repeat(51))).isEqualTo(FirstNameError.TooLong)
    }

    @Test
    fun `the length is counted after trimming`() {
        assertThat(ProfileIdentityRules.validateFirstName("  " + "a".repeat(50) + "  ")).isNull()
    }

    @Test
    fun `digits in a first name are refused`() {
        assertThat(ProfileIdentityRules.validateFirstName("Raj2")).isEqualTo(FirstNameError.InvalidCharacter)
    }

    /** "रघुवरन" carries vowel signs (category M); letters-only would refuse it. */
    @Test
    fun `a Devanagari first name is accepted`() {
        assertThat(ProfileIdentityRules.validateFirstName("रघुवरन")).isNull()
    }

    @Test
    fun `a typographic apostrophe is accepted`() {
        assertThat(ProfileIdentityRules.validateFirstName("O’Brien")).isNull()
    }

    @Test
    fun `the other allowed punctuation is accepted`() {
        assertThat(ProfileIdentityRules.validateFirstName("Anne-Marie O'Neil Jr.")).isNull()
        // ZWNJ and ZWJ, needed to type some Indic conjuncts.
        assertThat(ProfileIdentityRules.validateFirstName("क‍ष‌त")).isNull()
    }

    @Test
    fun `a control character is refused, even where trimming would strip it`() {
        assertThat(ProfileIdentityRules.validateFirstName("Ravi")).isEqualTo(FirstNameError.ControlCharacter)
        assertThat(ProfileIdentityRules.validateFirstName("Ravi\n")).isEqualTo(FirstNameError.ControlCharacter)
    }

    @Test
    fun `a whitespace-only first name is refused`() {
        assertThat(ProfileIdentityRules.validateFirstName("   ")).isEqualTo(FirstNameError.Empty)
        assertThat(ProfileIdentityRules.validateFirstName(" 　")).isEqualTo(FirstNameError.Empty)
    }

    @Test
    fun `a first name needs at least one letter`() {
        assertThat(ProfileIdentityRules.validateFirstName("-.'")).isEqualTo(FirstNameError.NoLetter)
    }

    /** Only U+0020 is an allowed space inside a name; a no-break space is not. */
    @Test
    fun `an inner no-break space is refused`() {
        assertThat(ProfileIdentityRules.validateFirstName("Devi Prasad")).isEqualTo(FirstNameError.InvalidCharacter)
    }

    @Test
    fun `an empty first name on an account with none stored is not a change`() {
        assertThat(ProfileIdentityRules.validateFirstNameChange(value = "", stored = "")).isNull()
        assertThat(ProfileIdentityRules.validateFirstNameChange(value = "  ", stored = " ")).isNull()
    }

    @Test
    fun `clearing a stored first name is refused`() {
        assertThat(ProfileIdentityRules.validateFirstNameChange(value = "", stored = "Ravi"))
            .isEqualTo(FirstNameError.Empty)
    }

    /** The server has no "unchanged" exemption for a non-empty name. */
    @Test
    fun `an unchanged stored name that breaks the rule is still refused`() {
        assertThat(ProfileIdentityRules.validateFirstNameChange(value = "Agent 007", stored = "Agent 007"))
            .isEqualTo(FirstNameError.InvalidCharacter)
    }

    // ── Date of birth ───────────────────────────────────────────────────

    /**
     * 19:00 UTC on 14 September is 00:30 IST on the 15th. A check that took
     * "today" in UTC would call a 2008-09-15 birthday 17, and the exactly-18
     * test below would fail.
     */
    private val clock = ProfileClock { Instant.parse("2026-09-14T19:00:00Z") }
    private val today = ProfileIdentityRules.todayInIndia(clock)

    @Test
    fun `today is taken in Asia Kolkata`() {
        assertThat(today).isEqualTo(LocalDate.of(2026, 9, 15))
    }

    @Test
    fun `exactly 18 today in India is accepted`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("2008-09-15", stored = "", today = today)).isNull()
    }

    @Test
    fun `17 years and 364 days is refused`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("2008-09-16", stored = "", today = today))
            .isEqualTo(DobError.UnderMinimumAge)
    }

    @Test
    fun `a future date of birth is refused`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("2026-09-16", stored = "", today = today))
            .isEqualTo(DobError.InFuture)
    }

    @Test
    fun `a date of birth in 1899 is refused`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("1899-12-31", stored = "", today = today))
            .isEqualTo(DobError.TooEarly)
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("1900-01-01", stored = "", today = today)).isNull()
    }

    @Test
    fun `a malformed or impossible date is refused`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("1990-02-30", stored = "", today = today))
            .isEqualTo(DobError.Invalid)
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("1990-3-16", stored = "", today = today))
            .isEqualTo(DobError.Invalid)
    }

    @Test
    fun `a blank date of birth is omitted, not checked`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("", stored = "1990-01-01", today = today)).isNull()
    }

    /** The server skips an unchanged DOB, so an older out-of-policy one must not block other edits. */
    @Test
    fun `an unchanged stored date of birth is not re-checked`() {
        assertThat(ProfileIdentityRules.validateDateOfBirthChange("2015-01-01", stored = "2015-01-01", today = today))
            .isNull()
    }

    @Test
    fun `the picker's latest date is the 18th birthday today in India`() {
        assertThat(ProfileIdentityRules.latestEligibleBirthDate(today)).isEqualTo(LocalDate.of(2008, 9, 15))
    }

    @Test
    fun `a 29 February birthday turns 18 on 1 March in a common year`() {
        val born = LocalDate.of(2008, 2, 29)
        assertThat(ProfileIdentityRules.ageOn(born, LocalDate.of(2026, 2, 28))).isEqualTo(17)
        assertThat(ProfileIdentityRules.ageOn(born, LocalDate.of(2026, 3, 1))).isEqualTo(18)
    }

    // ── Server refusals ─────────────────────────────────────────────────

    @Test
    fun `each server field code maps to its field and message`() {
        val expected = mapOf(
            "FIRST_NAME_INVALID" to (
                EditProfileField.FIRST_NAME to
                    "Use up to 50 characters: letters, spaces, hyphens, apostrophes and periods"
                ),
            "DOB_INVALID" to (EditProfileField.DATE_OF_BIRTH to "Pick a valid date of birth"),
            "DOB_REQUIRED" to (EditProfileField.DATE_OF_BIRTH to "Date of birth can't be removed"),
            "DOB_IN_FUTURE" to (EditProfileField.DATE_OF_BIRTH to "Date of birth can't be in the future"),
            "DOB_TOO_EARLY" to (EditProfileField.DATE_OF_BIRTH to "Date of birth can't be before 1 January 1900"),
            "DOB_UNDER_MINIMUM_AGE" to (EditProfileField.DATE_OF_BIRTH to "You must be at least 18 years old"),
            "DOB_MISMATCH_REGISTRATION" to (
                EditProfileField.DATE_OF_BIRTH to
                    "Date of birth can only be corrected by up to a year from the one you signed up with"
                ),
        )

        expected.forEach { (code, fieldAndMessage) ->
            assertWithMessage(code).that(ProfileIdentityRules.fieldErrorForCode(code)).isEqualTo(fieldAndMessage)
        }
    }

    @Test
    fun `an unknown or missing code maps to nothing`() {
        assertThat(ProfileIdentityRules.fieldErrorForCode("INVALID_PROFILE")).isNull()
        assertThat(ProfileIdentityRules.fieldErrorForCode(null)).isNull()
    }
}
