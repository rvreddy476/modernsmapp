package com.us.android.core.profile.data

import java.time.Instant
import java.time.LocalDate
import java.time.ZoneOffset
import java.time.format.DateTimeParseException

/**
 * Where "now" comes from for the identity-field rules. A seam rather than
 * `LocalDate.now()` so the 18th-birthday boundary can be tested on a pinned
 * instant, in India, regardless of the machine's zone.
 */
fun interface ProfileClock {
    fun now(): Instant

    companion object {
        val System = ProfileClock { Instant.now() }
    }
}

/**
 * Client mirror of profile-service's identity-field rules for
 * `PUT /v1/profiles/me` (identity-platform/services/profile-service/internal/
 * service/identity_fields.go, `NormalizeFirstName`, `ParseProfileDOB`,
 * `CheckProfileDOB`, `checkIdentityFields`).
 *
 * The server is the authority and re-checks everything, and one rule —
 * `DOB_MISMATCH_REGISTRATION` — can only be checked there, because it needs
 * the registration record. These exist so the form marks the field before a
 * round trip, and so a 422 that does come back lands on the right field.
 *
 * Deliberately NOT [com.us.android.core.auth]'s `RegistrationRules`: the
 * registration name rule is different (it allows any whitespace and no
 * period), and a profile save that borrowed it would be refused by this
 * endpoint.
 */
object ProfileIdentityRules {

    /** `maxFirstNameRunes`. Counted in code points, as Go counts runes. */
    const val MAX_FIRST_NAME_LENGTH = 50

    /** `MinimumAgeYears`. */
    const val MINIMUM_AGE_YEARS = 18

    /** `earliestDOB`. A fixed floor, not "120 years ago". */
    val EARLIEST_DATE_OF_BIRTH: LocalDate = LocalDate.of(1900, 1, 1)

    /**
     * `indiaTime`: a fixed +05:30, exactly as the server uses. India has no
     * DST, so this is Asia/Kolkata without depending on the device's tz data.
     */
    val INDIA_OFFSET: ZoneOffset = ZoneOffset.ofHoursMinutes(5, 30)

    // Stable wire codes (identity_fields.go). Never rename one.
    const val CODE_FIRST_NAME_INVALID = "FIRST_NAME_INVALID"
    const val CODE_DOB_INVALID = "DOB_INVALID"
    const val CODE_DOB_REQUIRED = "DOB_REQUIRED"
    const val CODE_DOB_IN_FUTURE = "DOB_IN_FUTURE"
    const val CODE_DOB_TOO_EARLY = "DOB_TOO_EARLY"
    const val CODE_DOB_UNDER_MINIMUM_AGE = "DOB_UNDER_MINIMUM_AGE"
    const val CODE_DOB_MISMATCH_REGISTRATION = "DOB_MISMATCH_REGISTRATION"

    /** The server's one message for any first-name refusal, since its code carries no sub-reason. */
    const val FIRST_NAME_REFUSED_MESSAGE =
        "Use up to 50 characters: letters, spaces, hyphens, apostrophes and periods"

    /** `todayInIndia`: the calendar date in Asia/Kolkata at this instant. */
    fun todayInIndia(clock: ProfileClock): LocalDate = clock.now().atOffset(INDIA_OFFSET).toLocalDate()

    /** The latest date of birth that is 18 or older on [today]. Bounds the picker. */
    fun latestEligibleBirthDate(today: LocalDate): LocalDate = today.minusYears(MINIMUM_AGE_YEARS.toLong())

    /**
     * `AgeOn`, copied exactly: completed years by explicit month/day
     * comparison, so a Feb 29 birthday is not 18 until Mar 1 in a common year.
     */
    fun ageOn(born: LocalDate, today: LocalDate): Int {
        var years = today.year - born.year
        if (today.monthValue < born.monthValue ||
            (today.monthValue == born.monthValue && today.dayOfMonth < born.dayOfMonth)
        ) {
            years--
        }
        return years
    }

    /**
     * `NormalizeFirstName`, in its order: control characters are refused
     * BEFORE trimming (so a tab is never trimmed into a pass), then 1–50 code
     * points after trimming, then the allowed character classes, then at
     * least one letter.
     */
    fun validateFirstName(raw: String): FirstNameError? {
        var index = 0
        while (index < raw.length) {
            val codePoint = raw.codePointAt(index)
            // A lone surrogate is not text; Go's utf8.ValidString refuses it.
            if (codePoint in Char.MIN_SURROGATE.code..Char.MAX_SURROGATE.code) return FirstNameError.NotText
            if (Character.getType(codePoint) == Character.CONTROL.toInt()) return FirstNameError.ControlCharacter
            index += Character.charCount(codePoint)
        }
        val name = raw.trim { it.isGoSpace() }
        if (name.isEmpty()) return FirstNameError.Empty
        if (name.codePointCount(0, name.length) > MAX_FIRST_NAME_LENGTH) return FirstNameError.TooLong

        var letters = 0
        index = 0
        while (index < name.length) {
            val codePoint = name.codePointAt(index)
            when {
                Character.isLetter(codePoint) -> letters++
                Character.getType(codePoint) in MARK_TYPES -> Unit
                codePoint in NAME_PUNCTUATION -> Unit
                else -> return FirstNameError.InvalidCharacter
            }
            index += Character.charCount(codePoint)
        }
        return if (letters == 0) FirstNameError.NoLetter else null
    }

    /**
     * `strings.TrimSpace`, the trim profile-service applies to a submitted
     * first name before comparing it with the stored one. The stored value is
     * compared as it is, untrimmed.
     */
    fun trimFirstName(raw: String): String = raw.trim { it.isGoSpace() }

    /**
     * The first-name rule as `checkIdentityFields` applies it to a written name.
     *
     * Its own exemption: an empty `first_name` (spaces only) on an account
     * with no stored name is "unchanged", because this client always sends
     * the key. It does NOT skip an unchanged non-empty name: the server skips
     * a submitted name whose [trimFirstName] equals the stored value, and the
     * caller applies that check before calling this.
     */
    fun validateFirstNameChange(value: String, stored: String): FirstNameError? {
        val error = validateFirstName(value) ?: return null
        val leftEmpty = value.trim(' ').isEmpty() && stored.trim { it.isGoSpace() }.isEmpty()
        return if (leftEmpty) null else error
    }

    /**
     * The date-of-birth rule as a save applies it.
     *
     * Blank is omitted from the request (see `ProfileRepository`), so it is
     * not checked. The same calendar date as [stored] is skipped server-side
     * ("not a DOB write"), so an older out-of-policy value does not block
     * other edits here either. Otherwise: `ParseProfileDOB`, then too early,
     * in the future, under 18 — in the server's order, with [today] in India.
     */
    fun validateDateOfBirthChange(value: String, stored: String, today: LocalDate): DobError? {
        if (value.isBlank()) return null
        val born = parseDate(value) ?: return DobError.Invalid
        if (parseDate(stored) == born) return null
        return when {
            born.isBefore(EARLIEST_DATE_OF_BIRTH) -> DobError.TooEarly
            born.isAfter(today) -> DobError.InFuture
            ageOn(born, today) < MINIMUM_AGE_YEARS -> DobError.UnderMinimumAge
            else -> null
        }
    }

    /**
     * A server 422 field code, as the field it names and the inline message.
     * Null for anything else, which the caller shows as its generic failure.
     */
    fun fieldErrorForCode(code: String?): Pair<EditProfileField, String>? = when (code) {
        CODE_FIRST_NAME_INVALID -> EditProfileField.FIRST_NAME to FIRST_NAME_REFUSED_MESSAGE
        CODE_DOB_INVALID -> EditProfileField.DATE_OF_BIRTH to DobError.Invalid.message
        CODE_DOB_REQUIRED -> EditProfileField.DATE_OF_BIRTH to DobError.Required.message
        CODE_DOB_IN_FUTURE -> EditProfileField.DATE_OF_BIRTH to DobError.InFuture.message
        CODE_DOB_TOO_EARLY -> EditProfileField.DATE_OF_BIRTH to DobError.TooEarly.message
        CODE_DOB_UNDER_MINIMUM_AGE -> EditProfileField.DATE_OF_BIRTH to DobError.UnderMinimumAge.message
        CODE_DOB_MISMATCH_REGISTRATION -> EditProfileField.DATE_OF_BIRTH to DobError.MismatchRegistration.message
        else -> null
    }

    enum class FirstNameError(val message: String) {
        NotText("Use letters for your first name"),
        ControlCharacter("First name can't contain tabs or line breaks"),
        Empty("Enter your first name"),
        TooLong("Use $MAX_FIRST_NAME_LENGTH characters or fewer"),
        InvalidCharacter("Use letters, spaces, hyphens, apostrophes and periods only"),
        NoLetter("First name must include a letter"),
    }

    enum class DobError(val message: String) {
        Invalid("Pick a valid date of birth"),
        Required("Date of birth can't be removed"),
        InFuture("Date of birth can't be in the future"),
        TooEarly("Date of birth can't be before 1 January 1900"),
        UnderMinimumAge("You must be at least $MINIMUM_AGE_YEARS years old"),
        MismatchRegistration(
            "Date of birth can only be corrected by up to a year from the one you signed up with",
        ),
    }

    private val MARK_TYPES = setOf(
        Character.NON_SPACING_MARK.toInt(),
        Character.COMBINING_SPACING_MARK.toInt(),
        Character.ENCLOSING_MARK.toInt(),
    )

    /** Space (U+0020 only), `-`, `'`, `.`, `’` (U+2019), ZWNJ (U+200C), ZWJ (U+200D). */
    private val NAME_PUNCTUATION = setOf(0x20, '-'.code, '\''.code, '.'.code, 0x2019, 0x200C, 0x200D)

    private val DATE_SHAPE = Regex("""^\d{4}-\d{2}-\d{2}$""")

    /** Exactly `YYYY-MM-DD` and a real calendar date (ISO_LOCAL_DATE is STRICT). */
    private fun parseDate(raw: String): LocalDate? {
        if (!DATE_SHAPE.matches(raw)) return null
        return try {
            LocalDate.parse(raw)
        } catch (_: DateTimeParseException) {
            null
        }
    }

    /** Go's `unicode.IsSpace`, which `strings.TrimSpace` trims. */
    private fun Char.isGoSpace(): Boolean = when (code) {
        0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000 -> true
        in 0x2000..0x200A -> true
        else -> false
    }
}
