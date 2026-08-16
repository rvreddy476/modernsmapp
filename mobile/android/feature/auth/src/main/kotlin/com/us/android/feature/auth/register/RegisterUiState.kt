package com.us.android.feature.auth.register

import com.us.android.core.auth.RegistrationRules
import com.us.android.core.designsystem.component.UsMessage

/**
 * Form state for account creation.
 *
 * Note the shape: **[firstName] and [lastName] are separate all the way
 * through** — separate inputs, separate errors, separate wire fields. There is
 * no display-name field anywhere in this flow, and nothing is concatenated on
 * submit. The server validates each name independently and rejects them with
 * distinct codes (`FIRST_NAME_REQUIRED`, `LAST_NAME_REQUIRED`), so joining
 * them would both fail the call and destroy the ability to mark the offending
 * input.
 *
 * Every field the server requires is represented here. Omitting one produces
 * a form that can never succeed, which is the worst kind of bug to ship:
 * email, password, first name, last name, date of birth, and an explicit
 * terms acceptance.
 */
data class RegisterUiState(
    val email: String = "",
    val firstName: String = "",
    val lastName: String = "",
    val password: String = "",
    /** `YYYY-MM-DD`, produced by the shared date picker — never typed by hand. */
    val dateOfBirth: String = "",
    /**
     * Required by product decision, NOT by the backend.
     *
     * The server column is `gender TEXT DEFAULT ''` with no enum, no NOT NULL
     * and no validation, so this rule lives entirely on the client and nothing
     * server-side will enforce it. Worth remembering if another client — or a
     * direct API call — ever creates an account without it.
     */
    val gender: RegistrationRules.Gender? = null,
    val acceptedTerms: Boolean = false,

    val emailError: String? = null,
    val firstNameError: String? = null,
    val lastNameError: String? = null,
    val passwordError: String? = null,
    val dateOfBirthError: String? = null,
    val genderError: String? = null,
    val termsError: String? = null,

    /**
     * Transient feedback shown in the shared message host — things with no
     * field to attach to: offline, rate limits, server faults.
     */
    val message: UsMessage? = null,

    val isSubmitting: Boolean = false,
    /** Set once the account exists and a verification email has been sent. */
    val verificationSent: Boolean = false,
    /**
     * The server-issued credential that authorises verifying THIS account.
     *
     * It is the only handle on the pending account — the verify and resend
     * endpoints take no user id by design. Losing it strands the signup, so
     * it is carried into the verification screen as a navigation argument.
     */
    val verificationToken: String = "",
) {
    val canSubmit: Boolean
        get() = !isSubmitting &&
            email.isNotBlank() &&
            firstName.isNotBlank() &&
            lastName.isNotBlank() &&
            password.isNotBlank() &&
            dateOfBirth.isNotBlank() &&
            gender != null &&
            acceptedTerms

    fun cleared() = copy(
        emailError = null,
        firstNameError = null,
        lastNameError = null,
        passwordError = null,
        dateOfBirthError = null,
        genderError = null,
        termsError = null,
        message = null,
    )

    /** True when any inline field error is showing. */
    val hasFieldErrors: Boolean
        get() = listOf(
            emailError,
            firstNameError,
            lastNameError,
            passwordError,
            dateOfBirthError,
            genderError,
            termsError,
        ).any { it != null }
}
