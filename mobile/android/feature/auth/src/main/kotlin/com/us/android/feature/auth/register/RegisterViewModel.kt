package com.us.android.feature.auth.register

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthErrorText
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.RegistrationRules
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class RegisterViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(RegisterUiState())
    val state: StateFlow<RegisterUiState> = _state.asStateFlow()

    fun onEmailChange(value: String) = _state.update { it.copy(email = value, emailError = null) }
    fun onFirstNameChange(value: String) =
        _state.update { it.copy(firstName = value, firstNameError = null) }
    fun onLastNameChange(value: String) =
        _state.update { it.copy(lastName = value, lastNameError = null) }
    fun onPasswordChange(value: String) =
        _state.update { it.copy(password = value, passwordError = null) }
    fun onDateOfBirthChange(value: String) =
        _state.update { it.copy(dateOfBirth = value, dateOfBirthError = null) }
    fun onGenderChange(value: RegistrationRules.Gender?) =
        _state.update { it.copy(gender = value, genderError = null) }
    fun onTermsChange(value: Boolean) =
        _state.update { it.copy(acceptedTerms = value, termsError = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val current = _state.value.cleared()
        val validated = validate(current)
        if (validated.hasFieldErrors) {
            // One summary message alongside the inline markers. On a form this
            // long the offending field is often scrolled out of view, so the
            // inline error alone can look like nothing happened.
            _state.value = validated.copy(
                message = UsMessage(
                    text = "Some details need fixing — check the highlighted fields.",
                    type = UsMessageType.Error,
                ),
            )
            return
        }

        _state.value = current.copy(isSubmitting = true)
        viewModelScope.launch {
            val result = authRepository.register(
                email = current.email,
                password = current.password,
                firstName = current.firstName,
                lastName = current.lastName,
                dateOfBirth = current.dateOfBirth,
                acceptedTerms = current.acceptedTerms,
                gender = current.gender?.wireValue,
            )
            _state.update { state ->
                when (result) {
                    // Registration returns no session — the account is PENDING
                    // until the emailed code is verified. Success here means
                    // "check your inbox", never "you are signed in".
                    is AppResult.Success -> state.copy(
                        isSubmitting = false,
                        verificationSent = true,
                        verificationToken =
                        (result.data as? SessionState.PendingVerification)?.token.orEmpty(),
                    )
                    is AppResult.Failure -> state.applyError(result.error)
                }
            }
        }
    }

    /**
     * Client-side pre-flight, mirroring the server's gate so the user gets a
     * marked field instead of a round trip. The server still re-checks
     * everything; [applyError] handles what comes back.
     */
    private fun validate(state: RegisterUiState): RegisterUiState {
        var next = state

        if (state.email.isBlank() || !state.email.contains('@')) {
            next = next.copy(emailError = "Enter a valid email address")
        }

        RegistrationRules.validateName(
            state.firstName,
            RegistrationRules.NameField.FIRST,
        )?.let { next = next.copy(firstNameError = it.message()) }

        RegistrationRules.validateName(
            state.lastName,
            RegistrationRules.NameField.LAST,
        )?.let { next = next.copy(lastNameError = it.message()) }

        RegistrationRules.validateDateOfBirth(state.dateOfBirth)?.let {
            next = next.copy(dateOfBirthError = it.message())
        }

        // Client-only rule — the backend accepts an absent gender.
        if (state.gender == null) {
            next = next.copy(genderError = "Select an option")
        }

        if (!state.acceptedTerms) {
            next = next.copy(termsError = "You must accept the terms to continue")
        }

        return next
    }

    /**
     * Routes a server rejection to the input that caused it, falling back to a
     * message when there is no single field to blame.
     *
     * The server uses a distinct code per field precisely so the client can do
     * this; collapsing them into one banner would leave the user guessing.
     */
    private fun RegisterUiState.applyError(error: AppError): RegisterUiState {
        val base = copy(isSubmitting = false)
        val code = when (error) {
            is AppError.Forbidden -> error.code
            is AppError.Unknown -> error.code
            else -> null
        }
        val target = code?.let(FIELD_FOR_CODE::get)
        val text = code?.let(AuthErrorText::registrationCodeText)

        return if (target != null && text != null) {
            base.withFieldError(target, text)
        } else {
            base.copy(
                message = UsMessage(
                    text = AuthErrorText.forRegister(error),
                    type = if (error is AppError.NoNetwork || error is AppError.Timeout) {
                        UsMessageType.Warning
                    } else {
                        UsMessageType.Error
                    },
                ),
            )
        }
    }

    private fun RegisterUiState.withFieldError(field: Field, text: String) = when (field) {
        Field.EMAIL -> copy(emailError = text)
        Field.FIRST_NAME -> copy(firstNameError = text)
        Field.LAST_NAME -> copy(lastNameError = text)
        Field.PASSWORD -> copy(passwordError = text)
        Field.DOB -> copy(dateOfBirthError = text)
        Field.GENDER -> copy(genderError = text)
        Field.TERMS -> copy(termsError = text)
    }

    private enum class Field { EMAIL, FIRST_NAME, LAST_NAME, PASSWORD, DOB, GENDER, TERMS }

    private companion object {
        /**
         * Which input each server code belongs to.
         *
         * `NAME_INVALID` is deliberately absent: the server does not say which
         * of the two names was invalid, so guessing would mark the wrong
         * field. It surfaces as a message instead.
         */
        val FIELD_FOR_CODE = mapOf(
            "EMAIL_REQUIRED" to Field.EMAIL,
            "USER_EXISTS" to Field.EMAIL,
            "FIRST_NAME_REQUIRED" to Field.FIRST_NAME,
            "LAST_NAME_REQUIRED" to Field.LAST_NAME,
            "WEAK_PASSWORD" to Field.PASSWORD,
            "INVALID_DOB" to Field.DOB,
            "UNDERAGE" to Field.DOB,
            "GENDER_REQUIRED" to Field.GENDER,
            "GENDER_INVALID" to Field.GENDER,
            "CONSENT_REQUIRED" to Field.TERMS,
        )
    }
}

private fun RegistrationRules.NameError.message(): String {
    val label = when (field) {
        RegistrationRules.NameField.FIRST -> "First name"
        RegistrationRules.NameField.LAST -> "Last name"
    }
    return when (this) {
        is RegistrationRules.NameError.Required -> "$label is required"
        is RegistrationRules.NameError.TooLong ->
            "$label must be ${RegistrationRules.MAX_NAME_LENGTH} characters or fewer"
        is RegistrationRules.NameError.Invalid ->
            "Use letters, spaces, hyphens or apostrophes only"
    }
}

private fun RegistrationRules.DobError.message(): String = when (this) {
    RegistrationRules.DobError.Required -> "Choose your date of birth"
    RegistrationRules.DobError.Malformed -> "That date isn't valid"
    RegistrationRules.DobError.InFuture -> "Date of birth can't be in the future"
    RegistrationRules.DobError.Underage ->
        "You need to be ${RegistrationRules.MINIMUM_AGE_YEARS} or older to sign up"
}
