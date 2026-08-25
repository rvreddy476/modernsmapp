package com.us.android.feature.auth.verify

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
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

data class VerifyEmailUiState(
    val code: String = "",
    val email: String = "",
    val codeError: String? = null,
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    val isResending: Boolean = false,
    /** Seconds until "Resend code" becomes tappable again. */
    val resendCooldownSeconds: Int = 0,
    val verified: Boolean = false,
) {
    val canSubmit: Boolean get() = !isSubmitting && code.length == CODE_LENGTH
    val canResend: Boolean get() = !isResending && !isSubmitting && resendCooldownSeconds == 0

    companion object {
        const val CODE_LENGTH = 6
    }
}

/**
 * Email verification.
 *
 * The account is identified ONLY by the server-issued verification token
 * passed in as a navigation argument. There is no user id anywhere in this
 * flow — the backend deliberately omits one so a caller cannot grind codes
 * against an account they name.
 *
 * On success this does NOT sign the user in: the endpoint issues no session,
 * so the screen routes to login.
 */
@HiltViewModel
class VerifyEmailViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(VerifyEmailUiState())
    val state: StateFlow<VerifyEmailUiState> = _state.asStateFlow()

    private var verificationToken: String = ""

    fun start(token: String, email: String) {
        verificationToken = token
        _state.update { it.copy(email = email) }
        startResendCooldown()
    }

    fun onCodeChange(value: String) =
        _state.update { it.copy(code = value, codeError = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        if (verificationToken.isBlank()) {
            _state.update {
                it.copy(
                    message = UsMessage(
                        "We've lost track of this signup. Please sign in to continue.",
                        UsMessageType.Error,
                    ),
                )
            }
            return
        }

        val code = _state.value.code
        _state.update { it.copy(isSubmitting = true, codeError = null, message = null) }

        viewModelScope.launch {
            when (val result = authRepository.verifyEmail(verificationToken, code)) {
                is AppResult.Success -> _state.update {
                    it.copy(isSubmitting = false, verified = true)
                }
                is AppResult.Failure -> _state.update {
                    it.copy(isSubmitting = false).applyError(result.error)
                }
            }
        }
    }

    fun resend() {
        if (verificationToken.isBlank() || !_state.value.canResend) return
        _state.update { it.copy(isResending = true, message = null, codeError = null) }

        viewModelScope.launch {
            when (val result = authRepository.resendVerification(verificationToken)) {
                is AppResult.Success -> {
                    _state.update {
                        it.copy(
                            isResending = false,
                            code = "",
                            message = UsMessage(
                                "We've sent a new code to ${it.email}.",
                                UsMessageType.Success,
                            ),
                        )
                    }
                    startResendCooldown()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(isResending = false).applyError(result.error)
                }
            }
        }
    }

    /**
     * Blocks resend for a minute after each send.
     *
     * The endpoint is rate-limited server-side; a visible countdown turns an
     * opaque 429 into something the user can wait out.
     */
    private fun startResendCooldown() {
        viewModelScope.launch {
            _state.update { it.copy(resendCooldownSeconds = RESEND_COOLDOWN_SECONDS) }
            while (_state.value.resendCooldownSeconds > 0) {
                delay(ONE_SECOND_MILLIS)
                _state.update { it.copy(resendCooldownSeconds = it.resendCooldownSeconds - 1) }
            }
        }
    }

    /**
     * The server returns ONE code for every verification failure — wrong,
     * expired, replayed, unknown token — so that a caller cannot probe which
     * codes or accounts exist. The client must not invent a more specific
     * message than the server was willing to give, so this names the likely
     * causes without claiming to know which one applies.
     */
    private fun VerifyEmailUiState.applyError(error: AppError): VerifyEmailUiState =
        when (error) {
            is AppError.NoNetwork -> copy(
                message = UsMessage(
                    "You're offline. Check your connection and try again.",
                    UsMessageType.Warning,
                ),
            )
            is AppError.Timeout -> copy(
                message = UsMessage(
                    "That took too long. Try again.",
                    UsMessageType.Warning,
                ),
            )
            is AppError.RateLimited -> copy(
                message = UsMessage(
                    "Too many attempts. Wait a moment before trying again.",
                    UsMessageType.Warning,
                ),
            )
            is AppError.Unknown, is AppError.InvalidRequest -> copy(
                code = "",
                codeError = "That code didn't work. It may have expired — " +
                    "codes last 5 minutes. Request a new one if needed.",
            )
            else -> copy(
                message = UsMessage(
                    "We couldn't verify that code. Please try again.",
                    UsMessageType.Error,
                ),
            )
        }

    private companion object {
        const val RESEND_COOLDOWN_SECONDS = 60
        const val ONE_SECOND_MILLIS = 1_000L
    }
}
