package com.us.android.feature.auth.reset

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthErrorText
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.PasswordRules
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

enum class ResetPasswordStep { Request, Reset, Done }

data class ResetPasswordUiState(
    val step: ResetPasswordStep = ResetPasswordStep.Request,
    /**
     * True when opened from Settings › Change password: the address is the
     * account's own and not editable, and the code is requested on entry.
     */
    val fromSettings: Boolean = false,
    val identifier: String = "",
    val identifierError: String? = null,
    val code: String = "",
    val codeError: String? = null,
    val newPassword: String = "",
    val newPasswordError: String? = null,
    val confirmPassword: String = "",
    val confirmPasswordError: String? = null,
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    val isResending: Boolean = false,
    val resendCooldownSeconds: Int = 0,
    val lockedOut: Boolean = false,
    /** "Sign in" was tapped on the Done step. */
    val finished: Boolean = false,
    /** The local session (if any) has been cleared; the route may leave. */
    val sessionEnded: Boolean = false,
) {
    val canRequest: Boolean get() = !isSubmitting && !lockedOut && identifier.isNotBlank()
    val canReset: Boolean
        get() = !isSubmitting && !lockedOut && code.length == CODE_LENGTH &&
            newPassword.isNotEmpty() && confirmPassword.isNotEmpty()
    val canResend: Boolean get() = !isResending && !isSubmitting && !lockedOut && resendCooldownSeconds == 0

    companion object {
        const val CODE_LENGTH = 6
    }
}

/**
 * Forgot password and, from Settings, change password: one flow, because the
 * server has one. There is no "current password → new password" route;
 * `POST /v1/auth/forgot-password` emails a code and `POST /v1/auth/reset-password`
 * consumes it, validates the new password, sets it and revokes every session
 * in one transaction (auth.go:1215). The web's Security › Change password
 * card does exactly this.
 *
 * Copy rule for step one: the server always answers 200 to prevent account
 * enumeration, so the screen says "if an account exists we've sent a code"
 * and never "sent" or "no such account". Recovery is email-only at launch —
 * a phone number is accepted and silently delivers nothing — so the field
 * asks for the email.
 *
 * The password and code live only in this state: never logged, never put in
 * the message host, cleared when the step completes.
 */
@HiltViewModel
class ResetPasswordViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ResetPasswordUiState())
    val state: StateFlow<ResetPasswordUiState> = _state.asStateFlow()

    private var started = false

    /**
     * [identifier] pre-fills the address. [fromSettings] locks it and sends
     * the code straight away, since a signed-in person asking to change their
     * password has already named the account.
     */
    fun start(identifier: String, fromSettings: Boolean) {
        if (started) return
        started = true
        _state.update { it.copy(identifier = identifier.trim(), fromSettings = fromSettings) }
        if (fromSettings && identifier.isNotBlank()) requestCode()
    }

    fun onIdentifierChange(value: String) = _state.update { it.copy(identifier = value, identifierError = null) }

    fun onCodeChange(value: String) = _state.update {
        it.copy(code = value.filter(Char::isDigit).take(ResetPasswordUiState.CODE_LENGTH), codeError = null)
    }

    fun onNewPasswordChange(value: String) = _state.update {
        it.copy(newPassword = value, newPasswordError = null, confirmPasswordError = null)
    }

    fun onConfirmPasswordChange(value: String) = _state.update {
        it.copy(confirmPassword = value, confirmPasswordError = null)
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Back from the code step to the address step, keeping the address. */
    fun editIdentifier() = _state.update {
        if (it.fromSettings) it else it.copy(step = ResetPasswordStep.Request, code = "", codeError = null, message = null)
    }

    /** Step one: ask for a code. Moves to step two on ANY 200 — see the class note. */
    fun requestCode() {
        val current = _state.value
        if (!current.canRequest) return
        val identifier = current.identifier.trim()
        if (!identifier.looksLikeEmail()) {
            _state.update { it.copy(identifierError = "Enter the email address you signed up with.") }
            return
        }
        _state.update { it.copy(isSubmitting = true, identifierError = null, message = null) }
        viewModelScope.launch {
            when (val result = authRepository.forgotPassword(identifier)) {
                is AppResult.Success -> {
                    _state.update {
                        it.copy(
                            isSubmitting = false,
                            step = ResetPasswordStep.Reset,
                            message = UsMessage(sentText(identifier), UsMessageType.Info),
                        )
                    }
                    startResendCooldown()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(isSubmitting = false).applyError(result.error, AuthErrorText::forForgotPassword)
                }
            }
        }
    }

    fun resend() {
        val current = _state.value
        if (!current.canResend) return
        _state.update { it.copy(isResending = true, message = null, codeError = null) }
        viewModelScope.launch {
            when (val result = authRepository.forgotPassword(current.identifier)) {
                is AppResult.Success -> {
                    _state.update {
                        it.copy(isResending = false, code = "", message = UsMessage(sentText(it.identifier), UsMessageType.Info))
                    }
                    startResendCooldown()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(isResending = false).applyError(result.error, AuthErrorText::forForgotPassword)
                }
            }
        }
    }

    /** Step two: the code and the new password. Client rules first, server rules second. */
    fun submitNewPassword() {
        val current = _state.value
        if (!current.canReset) return
        val ruleError = PasswordRules.validate(current.newPassword)
        when {
            ruleError != null -> {
                _state.update { it.copy(newPasswordError = PasswordRules.text(ruleError)) }
                return
            }
            current.newPassword != current.confirmPassword -> {
                _state.update { it.copy(confirmPasswordError = "Those passwords don't match.") }
                return
            }
        }
        _state.update { it.copy(isSubmitting = true, message = null, codeError = null, newPasswordError = null) }
        viewModelScope.launch {
            val result = authRepository.resetPassword(current.identifier, current.code, current.newPassword)
            _state.update { state ->
                when (result) {
                    is AppResult.Success -> state.copy(
                        isSubmitting = false,
                        step = ResetPasswordStep.Done,
                        code = "",
                        newPassword = "",
                        confirmPassword = "",
                    )
                    is AppResult.Failure -> state.copy(isSubmitting = false).applyResetError(result.error)
                }
            }
        }
    }

    /**
     * "Sign in" on the Done step. From Settings the server has already
     * revoked this session; clearing it locally is what flips the shell to
     * Login — held back until now so the confirmation was actually seen.
     * From sign-in there is no session, and the call is a no-op.
     */
    fun finish() {
        if (_state.value.finished) return
        _state.update { it.copy(finished = true) }
        viewModelScope.launch {
            authRepository.endSessionAfterPasswordReset()
            _state.update { it.copy(sessionEnded = true) }
        }
    }

    private fun ResetPasswordUiState.applyResetError(error: AppError): ResetPasswordUiState = when {
        error is AppError.Unknown && error.code == WEAK_PASSWORD ->
            copy(newPasswordError = AuthErrorText.forResetPassword(error))
        error is AppError.Unknown || error is AppError.InvalidRequest ->
            copy(code = "", codeError = AuthErrorText.forResetPassword(error))
        else -> applyError(error, AuthErrorText::forResetPassword)
    }

    private fun ResetPasswordUiState.applyError(
        error: AppError,
        text: (AppError) -> String,
    ): ResetPasswordUiState = when (error) {
        is AppError.RateLimited -> copy(lockedOut = true, message = UsMessage(text(error), UsMessageType.Warning))
        is AppError.NoNetwork, is AppError.Timeout -> copy(message = UsMessage(text(error), UsMessageType.Warning))
        else -> copy(message = UsMessage(text(error), UsMessageType.Error))
    }

    private fun startResendCooldown() {
        viewModelScope.launch {
            _state.update { it.copy(resendCooldownSeconds = RESEND_COOLDOWN_SECONDS) }
            while (_state.value.resendCooldownSeconds > 0) {
                delay(ONE_SECOND_MILLIS)
                _state.update { it.copy(resendCooldownSeconds = it.resendCooldownSeconds - 1) }
            }
        }
    }

    private fun sentText(identifier: String): String =
        "If an account exists for $identifier, we've emailed a 6-digit code. It expires in a few minutes."

    private fun String.looksLikeEmail(): Boolean = contains('@') && substringAfter('@').contains('.')

    private companion object {
        const val WEAK_PASSWORD = "WEAK_PASSWORD"
        const val RESEND_COOLDOWN_SECONDS = 60
        const val ONE_SECOND_MILLIS = 1_000L
    }
}
