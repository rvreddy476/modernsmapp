package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.LocalDate

/**
 * Pins the client-side mirror of the server's registration gate.
 *
 * These rules are duplicated from Go on purpose (to fail fast and mark the
 * offending field), which means they can silently drift. These tests are what
 * make the drift visible: if the server changes, one of these should be the
 * thing that fails.
 */
class RegistrationRulesTest {

    private val today = LocalDate.of(2026, 8, 16)

    // ── Names ───────────────────────────────────────────────────────────

    @Test
    fun `ordinary names pass`() {
        assertThat(RegistrationRules.validateName("Raghu", FIRST)).isNull()
        assertThat(RegistrationRules.validateName("Varan", LAST)).isNull()
    }

    @Test
    fun `Indic names with vowel signs and viramas are accepted`() {
        // Regression guard for a real defect, fixed on both sides.
        // Category-L-only validation ("letters") silently means "no combining
        // marks", which rejects most Devanagari, Telugu and Tamil names —
        // रघुवरन fails on U+0941, ரகுவரன் on U+0BC1. The server's mirror of
        // this test is TestValidatePersonName in auth-service.
        assertThat(RegistrationRules.validateName("रघुवरन", FIRST)).isNull()
        assertThat(RegistrationRules.validateName("रमेश", FIRST)).isNull()
        assertThat(RegistrationRules.validateName("రఘువరన్", FIRST)).isNull()
        assertThat(RegistrationRules.validateName("ரகுவரன்", LAST)).isNull()
    }

    @Test
    fun `mark-free Indic names are accepted too`() {
        // These are the only Indic names the server currently lets through,
        // which is exactly why the bug went unnoticed.
        assertThat(RegistrationRules.validateName("कमल", FIRST)).isNull()
    }

    @Test
    fun `combining marks do not open the door to symbols or digits`() {
        // Widening to category M must not accidentally widen to everything.
        assertThat(RegistrationRules.validateName("Raghu#", FIRST))
            .isInstanceOf(RegistrationRules.NameError.Invalid::class.java)
        assertThat(RegistrationRules.validateName("रघु1", FIRST))
            .isInstanceOf(RegistrationRules.NameError.Invalid::class.java)
        assertThat(RegistrationRules.validateName("Raghu😀", FIRST))
            .isInstanceOf(RegistrationRules.NameError.Invalid::class.java)
    }

    @Test
    fun `spaces hyphens and apostrophes are allowed`() {
        assertThat(RegistrationRules.validateName("Devi Prasad", FIRST)).isNull()
        assertThat(RegistrationRules.validateName("Anne-Marie", FIRST)).isNull()
        assertThat(RegistrationRules.validateName("O'Brien", LAST)).isNull()
    }

    @Test
    fun `digits are rejected`() {
        // A name field that accepts "user123" is how handles end up in it,
        // and a handle is a separate, uniqueness-checked concept.
        assertThat(RegistrationRules.validateName("user123", FIRST))
            .isInstanceOf(RegistrationRules.NameError.Invalid::class.java)
    }

    @Test
    fun `blank and whitespace-only names are Required, not Invalid`() {
        // binding:"required" alone accepts a string of spaces server-side,
        // which is why the server re-checks after trimming. So do we.
        assertThat(RegistrationRules.validateName("", FIRST))
            .isInstanceOf(RegistrationRules.NameError.Required::class.java)
        assertThat(RegistrationRules.validateName("   ", FIRST))
            .isInstanceOf(RegistrationRules.NameError.Required::class.java)
    }

    @Test
    fun `names longer than 50 characters are rejected`() {
        assertThat(RegistrationRules.validateName("a".repeat(51), LAST))
            .isInstanceOf(RegistrationRules.NameError.TooLong::class.java)
        assertThat(RegistrationRules.validateName("a".repeat(50), LAST)).isNull()
    }

    @Test
    fun `the error carries the field that caused it`() {
        val error = RegistrationRules.validateName("", LAST)
        assertThat(error?.field).isEqualTo(LAST)
    }

    // ── Date of birth ───────────────────────────────────────────────────

    @Test
    fun `a birth date exactly 18 years ago is eligible`() {
        // The boundary is the whole point of the gate; off-by-one here means
        // turning away users on their birthday.
        val exactly18 = LocalDate.of(2008, 8, 16)
        assertThat(RegistrationRules.validateDateOfBirth(exactly18.toString(), today)).isNull()
    }

    @Test
    fun `one day short of 18 is Underage`() {
        val dayShort = LocalDate.of(2008, 8, 17)
        assertThat(RegistrationRules.validateDateOfBirth(dayShort.toString(), today))
            .isEqualTo(RegistrationRules.DobError.Underage)
    }

    @Test
    fun `an absent date of birth is a rejection, never a pass`() {
        // This is exactly how the old server-side gate was bypassed.
        assertThat(RegistrationRules.validateDateOfBirth("", today))
            .isEqualTo(RegistrationRules.DobError.Required)
    }

    @Test
    fun `an unparseable date is Malformed`() {
        assertThat(RegistrationRules.validateDateOfBirth("16-08-2008", today))
            .isEqualTo(RegistrationRules.DobError.Malformed)
        assertThat(RegistrationRules.validateDateOfBirth("not a date", today))
            .isEqualTo(RegistrationRules.DobError.Malformed)
    }

    @Test
    fun `a future date is rejected rather than computing a negative age`() {
        assertThat(RegistrationRules.validateDateOfBirth("2030-01-01", today))
            .isEqualTo(RegistrationRules.DobError.InFuture)
    }

    @Test
    fun `latestEligibleBirthDate is the newest date the calendar may offer`() {
        val latest = RegistrationRules.latestEligibleBirthDate(today)

        assertThat(latest).isEqualTo(LocalDate.of(2008, 8, 16))
        // Anything the picker allows must also pass validation — otherwise the
        // calendar would offer a date the form then rejects.
        assertThat(RegistrationRules.validateDateOfBirth(latest.toString(), today)).isNull()
        assertThat(RegistrationRules.validateDateOfBirth(latest.plusDays(1).toString(), today))
            .isEqualTo(RegistrationRules.DobError.Underage)
    }

    // ── Gender ──────────────────────────────────────────────────────────

    @Test
    fun `gender wire values are the stored tokens and must not drift`() {
        // These must match auth-service's `genderValues` exactly. The server
        // now rejects anything outside that closed set with GENDER_INVALID,
        // so drift here turns into a registration that cannot succeed.
        assertThat(RegistrationRules.Gender.MALE.wireValue).isEqualTo("male")
        assertThat(RegistrationRules.Gender.FEMALE.wireValue).isEqualTo("female")
        assertThat(RegistrationRules.Gender.OTHER.wireValue).isEqualTo("other")
        assertThat(RegistrationRules.Gender.entries).hasSize(3)
    }

    @Test
    fun `fromWire round-trips and tolerates case and padding`() {
        RegistrationRules.Gender.entries.forEach { gender ->
            assertThat(RegistrationRules.Gender.fromWire(gender.wireValue)).isEqualTo(gender)
        }
        assertThat(RegistrationRules.Gender.fromWire(" Male "))
            .isEqualTo(RegistrationRules.Gender.MALE)
    }

    @Test
    fun `fromWire returns null for absent or unknown values`() {
        // Gender is optional, and the column defaults to an empty string —
        // so "no answer" must map to null rather than blowing up.
        assertThat(RegistrationRules.Gender.fromWire(null)).isNull()
        assertThat(RegistrationRules.Gender.fromWire("")).isNull()
        assertThat(RegistrationRules.Gender.fromWire("unspecified")).isNull()
    }

    // ── Consent ─────────────────────────────────────────────────────────

    @Test
    fun `terms version matches the server constant`() {
        // Drift here is recoverable but noisy: the server refuses with
        // CONSENT_REQUIRED and returns the correct version in error.details.
        assertThat(RegistrationRules.TERMS_VERSION).isEqualTo("2026-08-01")
    }

    @Test
    fun `the age floor is 18, not 13`() {
        // Not a typo to be "corrected": there is no verifiable
        // parental-consent flow, so the platform cannot lawfully onboard a
        // minor at all.
        assertThat(RegistrationRules.MINIMUM_AGE_YEARS).isEqualTo(18)
    }

    private companion object {
        val FIRST = RegistrationRules.NameField.FIRST
        val LAST = RegistrationRules.NameField.LAST
    }
}
