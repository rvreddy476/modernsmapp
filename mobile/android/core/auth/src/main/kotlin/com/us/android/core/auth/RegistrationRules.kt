package com.us.android.core.auth

import java.time.LocalDate
import java.time.Period
import java.time.format.DateTimeParseException

/**
 * Client-side mirror of the server's registration gate.
 *
 * These rules are duplicated from the Go source deliberately, for one reason
 * only: to fail fast and mark the offending field before a round trip. The
 * **server remains the authority** — every rule here is re-checked there, and
 * the UI must still handle the server's rejection codes, because a client
 * check can drift and a client check can be bypassed.
 *
 * Mirrors:
 *  - `validatePersonName`  (handler.go:440)
 *  - `ParseDOB` / `AgeOn`  (eligibility.go:83)
 *  - `MinimumAgeYears`, `CurrentTermsVersion` (eligibility.go:39,44)
 */
object RegistrationRules {

    /**
     * The terms version a registration must accept.
     *
     * If this drifts from the server the registration is refused with
     * `CONSENT_REQUIRED`, and the error's `details.current_terms_version`
     * carries the correct value — so the failure is recoverable rather than
     * a dead form.
     */
    const val TERMS_VERSION = "2026-08-01"

    /**
     * 18, not 13. The platform has no verifiable parental-consent flow, so it
     * cannot lawfully process a minor's data.
     */
    const val MINIMUM_AGE_YEARS = 18

    const val MAX_NAME_LENGTH = 50

    /**
     * The latest date of birth that satisfies the 18+ gate, as of [today].
     *
     * Used to bound the calendar so an underage date cannot be picked at all,
     * rather than being rejected after the fact.
     */
    fun latestEligibleBirthDate(today: LocalDate = LocalDate.now()): LocalDate =
        today.minusYears(MINIMUM_AGE_YEARS.toLong())

    /**
     * Validates a human name.
     *
     * Unicode letters, not ASCII: the product is India-first, so Devanagari,
     * Telugu and Tamil names must pass exactly as Latin ones do. Spaces,
     * hyphens and apostrophes are allowed for "O'Brien", "Devi Prasad",
     * "Anne-Marie". Digits are rejected — a name field that accepts "user123"
     * is how handles end up in it, and a handle is a separate concept.
     *
     * Combining marks (Unicode category M) are accepted, and that is
     * load-bearing rather than incidental. `isLetter()` alone covers category
     * L only, so it accepts the consonant stems of an Indic name and rejects
     * its vowel signs — "रघुवरन" fails on U+0941, "ரகுவரன்" on U+0BC1 — while
     * mark-free names like "कमल" pass, which is how the gap hides.
     *
     * The server had exactly this bug in `validatePersonName` and it is now
     * fixed (`unicode.IsMark` added, covered by TestValidatePersonName).
     * Client and server agree; both sides have regression tests, because the
     * failure mode is silent and only affects names nobody on the team
     * happens to be typing.
     */
    fun validateName(raw: String, field: NameField): NameError? {
        val name = raw.trim()
        if (name.isEmpty()) return NameError.Required(field)
        if (name.codePointCount(0, name.length) > MAX_NAME_LENGTH) {
            return NameError.TooLong(field)
        }
        val illegal = name.any { ch -> !ch.isAllowedInName() }
        return if (illegal) NameError.Invalid(field) else null
    }

    private fun Char.isAllowedInName(): Boolean = when {
        isLetter() -> true
        isWhitespace() -> true
        this == '-' || this == '\'' -> true
        // Combining marks: Indic matras, viramas, nuktas. Without these,
        // "letters only" silently means "Latin and abugida stems only".
        category in NAME_MARK_CATEGORIES -> true
        else -> false
    }

    private val NAME_MARK_CATEGORIES = setOf(
        CharCategory.NON_SPACING_MARK,
        CharCategory.COMBINING_SPACING_MARK,
        CharCategory.ENCLOSING_MARK,
    )

    /**
     * Validates a `YYYY-MM-DD` date of birth and the 18+ gate.
     *
     * Every failure path is a rejection; there is no branch that lets a
     * missing or unparseable value through. That is precisely how the old
     * server-side gate was bypassed.
     */
    fun validateDateOfBirth(raw: String, today: LocalDate = LocalDate.now()): DobError? {
        val value = raw.trim()
        if (value.isEmpty()) return DobError.Required
        val born = try {
            LocalDate.parse(value)
        } catch (_: DateTimeParseException) {
            return DobError.Malformed
        }
        if (born.isAfter(today)) return DobError.InFuture
        if (Period.between(born, today).years < MINIMUM_AGE_YEARS) return DobError.Underage
        return null
    }

    /**
     * The gender vocabulary.
     *
     * The server stores this as a free-text column (`gender TEXT DEFAULT ''`)
     * with no enum and no validation, so the **client owns these tokens** and
     * nothing on the backend will catch a typo. Hence a sealed set rather
     * than raw strings at call sites.
     *
     * [wireValue] is what gets stored and must never change once data exists;
     * the user-facing label is presentation and lives in the UI layer.
     * Optional, matching the server — an empty value is stored as NULL.
     */
    enum class Gender(val wireValue: String) {
        MALE("male"),
        FEMALE("female"),
        OTHER("other"),
        ;

        companion object {
            fun fromWire(value: String?): Gender? =
                entries.firstOrNull { it.wireValue == value?.trim()?.lowercase() }
        }
    }

    enum class NameField { FIRST, LAST }

    sealed interface NameError {
        val field: NameField

        data class Required(override val field: NameField) : NameError
        data class TooLong(override val field: NameField) : NameError
        data class Invalid(override val field: NameField) : NameError
    }

    enum class DobError { Required, Malformed, InFuture, Underage }
}
