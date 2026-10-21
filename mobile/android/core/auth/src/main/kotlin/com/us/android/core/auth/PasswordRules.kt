package com.us.android.core.auth

/**
 * Client-side mirror of the server's password gate.
 *
 * Mirrors `validatePassword` in auth-service/internal/service/auth.go:421 —
 * at least [MIN_LENGTH] characters, one upper-case letter, one digit and one
 * character from [SPECIAL_CHARACTERS] — so a form can mark the field before
 * the round trip and show the rule the server would otherwise state in a 422
 * `WEAK_PASSWORD`. The **server remains the authority**: it re-checks every
 * rule, and the screens still handle its rejection, because a client check
 * can drift and can be bypassed.
 *
 * The special-character set is copied byte for byte from the Go regexp
 * `[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]`. A wider set here would accept a
 * password the server refuses, and the user would be told it was weak after
 * being told it was fine.
 */
object PasswordRules {

    const val MIN_LENGTH = 8

    const val SPECIAL_CHARACTERS = "!@#$%^&*()_+-=[]{};':\"\\|,.<>/?"

    /** The rule as one sentence, for the helper text under a new-password field. */
    const val RULES_TEXT =
        "At least $MIN_LENGTH characters, with an upper-case letter, a number and a symbol."

    /** The FIRST rule [password] breaks, in the server's order, or null when it passes. */
    fun validate(password: String): PasswordError? = when {
        password.length < MIN_LENGTH -> PasswordError.TooShort
        password.none { it.isUpperCase() && it in 'A'..'Z' } -> PasswordError.NeedsUppercase
        password.none { it in '0'..'9' } -> PasswordError.NeedsDigit
        password.none { it in SPECIAL_CHARACTERS } -> PasswordError.NeedsSpecial
        else -> null
    }

    /** What to show under the field. */
    fun text(error: PasswordError): String = when (error) {
        PasswordError.TooShort -> "Use at least $MIN_LENGTH characters."
        PasswordError.NeedsUppercase -> "Add an upper-case letter (A–Z)."
        PasswordError.NeedsDigit -> "Add a number (0–9)."
        PasswordError.NeedsSpecial -> "Add a symbol, such as ! @ # or ?."
    }

    enum class PasswordError { TooShort, NeedsUppercase, NeedsDigit, NeedsSpecial }
}
