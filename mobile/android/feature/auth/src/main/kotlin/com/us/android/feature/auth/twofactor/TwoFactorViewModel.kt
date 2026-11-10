package com.us.android.feature.auth.twofactor

import androidx.annotation.StringRes
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthErrorText
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.TwoFactorCodeSent
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.R
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * What the emailed-code screen says, from resources. [inline] notices sit
 * under the code field; the rest go to the message host as [type].
 */
enum class EmailCodeNotice(@StringRes val text: Int, val type: UsMessageType, val inline: Boolean = false) {
    WrongCode(R.string.two_factor_email_wrong_code, UsMessageType.Error, inline = true),
    CodeSent(R.string.two_factor_email_code_sent, UsMessageType.Success),
    ResendTooSoon(R.string.two_factor_email_resend_too_soon, UsMessageType.Warning),
    EmailUnavailable(R.string.two_factor_email_unavailable, UsMessageType.Error),
    SignInExpired(R.string.two_factor_email_sign_in_expired, UsMessageType.Error),
}

data class TwoFactorUiState(
    val code: String = "",
    /** True while the field takes a recovery code (`xxxx-xxxx-xxxx`) instead of six digits. */
    val useRecoveryCode: Boolean = false,
    val codeError: String? = null,
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    /** Set once the session is stored; the shell flips on the session state, this just confirms. */
    val signedIn: Boolean = false,
    /**
     * Set when the server has locked this account out (429 from the login
     * limiter: five tries per account per fifteen minutes). The form stays
     * disabled rather than inviting a sixth try that cannot succeed.
     */
    val lockedOut: Boolean = false,
    /**
     * The code was emailed (`two_factor_method` `email`, founder 10 Nov
     * 2026) rather than shown by an authenticator app. There are no recovery
     * codes on this path; there is a Resend.
     */
    val isEmailCode: Boolean = false,
    /** Where the code went, masked by the server; blank when it did not say. */
    val emailMasked: String = "",
    /** Seconds until Resend is allowed again; 0 means now. */
    val resendInSeconds: Int = 0,
    val isResending: Boolean = false,
    val emailNotice: EmailCodeNotice? = null,
) {
    val canSubmit: Boolean
        get() = !isSubmitting && !lockedOut && !signedIn &&
            if (useRecoveryCode) code.isNotBlank() else code.length == CODE_LENGTH

    val canResend: Boolean
        get() = isEmailCode && resendInSeconds == 0 && !isResending && !isSubmitting && !lockedOut && !signedIn

    companion object {
        const val CODE_LENGTH = 6

        /** The server's cooldown between emailed codes, used when a response does not name one. */
        const val RESEND_COOLDOWN_SECONDS = 60
    }
}

/**
 * The second step of a sign-in that came back `requires_2fa`.
 *
 * The pending token and the user id arrive as navigation arguments from the
 * login response; the code never leaves this state — it is not logged, not
 * put in the message host, and cleared from the field after every attempt
 * so a wrong code is not resubmitted by a second tap.
 *
 * Two kinds of code reach this screen. An emailed code (the default for app
 * users since 10 Nov 2026) is already in the inbox when the screen opens, so
 * the Resend timer starts at once. An authenticator-app code (admins, and
 * app users who turned it on before the change) keeps the recovery-code
 * option. An unknown or missing method is an older server: authenticator.
 *
 * Success stores the session (`SessionManager.applyAuthResponse`) and the
 * shell flips on the session state; nothing here navigates into the app.
 */
@HiltViewModel
class TwoFactorViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(TwoFactorUiState())
    val state: StateFlow<TwoFactorUiState> = _state.asStateFlow()

    private var userId: String = ""
    private var pendingToken: String = ""
    private var countdown: Job? = null

    fun start(userId: String, pendingToken: String, method: String = "", emailMasked: String = "") {
        // The route re-runs this on recomposition; the same attempt must not
        // restart its Resend timer.
        if (pendingToken.isNotBlank() && pendingToken == this.pendingToken) return
        this.userId = userId
        this.pendingToken = pendingToken
        val emailed = method.trim() == SessionState.PendingTwoFactor.METHOD_EMAIL
        _state.update { it.copy(isEmailCode = emailed, emailMasked = emailMasked.trim(), useRecoveryCode = false) }
        // The server mailed the first code with the login response.
        if (emailed) startCountdown(TwoFactorUiState.RESEND_COOLDOWN_SECONDS)
    }

    fun onCodeChange(value: String) = _state.update { current ->
        val next = if (current.useRecoveryCode) {
            value.trim()
        } else {
            value.filter(Char::isDigit).take(TwoFactorUiState.CODE_LENGTH)
        }
        current.copy(
            code = next,
            codeError = null,
            emailNotice = current.emailNotice?.takeUnless { it.inline },
        )
    }

    /** Switches between the six-digit field and the recovery-code field, clearing either. */
    fun toggleRecoveryCode() = _state.update {
        if (it.isEmailCode) return@update it
        it.copy(useRecoveryCode = !it.useRecoveryCode, code = "", codeError = null, message = null)
    }

    fun dismissMessage() = _state.update {
        it.copy(message = null, emailNotice = it.emailNotice?.takeIf { notice -> notice.inline })
    }

    fun submit() {
        val current = _state.value
        if (!current.canSubmit) return
        if (pendingToken.isBlank() || userId.isBlank()) {
            _state.update { it.copy(message = UsMessage(LOST_SIGN_IN_TEXT, UsMessageType.Error)) }
            return
        }
        _state.update { it.copy(isSubmitting = true, codeError = null, message = null, emailNotice = null) }
        viewModelScope.launch {
            when (val result = authRepository.verifyTwoFactor(userId, pendingToken, current.code)) {
                is AppResult.Success -> _state.update { it.copy(isSubmitting = false).applySession(result.data) }
                is AppResult.Failure -> _state.update {
                    it.copy(isSubmitting = false, code = "").applyError(result.error)
                }
            }
        }
    }

    /** Emailed codes only: asks for a new one once the cooldown has run out. */
    fun resend() {
        if (!_state.value.canResend) return
        if (pendingToken.isBlank()) {
            _state.update { it.copy(emailNotice = EmailCodeNotice.SignInExpired) }
            return
        }
        _state.update { it.copy(isResending = true, emailNotice = null, message = null) }
        viewModelScope.launch {
            when (val result = authRepository.resendTwoFactorCode(pendingToken)) {
                is AppResult.Success -> onResent(result.data)
                is AppResult.Failure -> onResendFailed(result.error)
            }
        }
    }

    private fun onResent(sent: TwoFactorCodeSent) {
        _state.update {
            it.copy(
                isResending = false,
                code = "",
                codeError = null,
                emailMasked = sent.emailMasked.trim().ifBlank { it.emailMasked },
                emailNotice = EmailCodeNotice.CodeSent,
            )
        }
        startCountdown(sent.resendAfterSeconds.takeIf { it > 0 } ?: TwoFactorUiState.RESEND_COOLDOWN_SECONDS)
    }

    private fun onResendFailed(error: AppError) {
        _state.update { it.copy(isResending = false) }
        when {
            error is AppError.RateLimited -> {
                _state.update { it.copy(emailNotice = EmailCodeNotice.ResendTooSoon) }
                startCountdown(
                    error.retryAfterSeconds?.takeIf { it > 0 }?.toInt() ?: TwoFactorUiState.RESEND_COOLDOWN_SECONDS,
                )
            }
            // The pending sign-in is gone (expired, or spent by too many tries).
            error is AppError.AuthFailed -> _state.update { it.copy(emailNotice = EmailCodeNotice.SignInExpired) }
            // An authenticator-app sign-in after all: the server never mails it.
            error.code() == CODE_METHOD_MISMATCH -> {
                countdown?.cancel()
                _state.update { it.copy(isEmailCode = false, resendInSeconds = 0, code = "") }
            }
            error.code() == CODE_EMAIL_UNAVAILABLE ->
                _state.update { it.copy(emailNotice = EmailCodeNotice.EmailUnavailable) }
            else -> _state.update {
                it.copy(message = UsMessage(AuthErrorText.forLogin(error), error.messageType()))
            }
        }
    }

    private fun startCountdown(seconds: Int) {
        countdown?.cancel()
        _state.update { it.copy(resendInSeconds = seconds.coerceAtLeast(0)) }
        if (seconds <= 0) return
        countdown = viewModelScope.launch {
            while (_state.value.resendInSeconds > 0) {
                delay(TICK_MS)
                _state.update { it.copy(resendInSeconds = (it.resendInSeconds - 1).coerceAtLeast(0)) }
            }
        }
    }

    private fun TwoFactorUiState.applySession(session: SessionState): TwoFactorUiState = when (session) {
        is SessionState.Authenticated -> copy(signedIn = true)
        // A 200 that is not a session: the server should not do this after a
        // second factor, and the only safe reading is "start again".
        else -> copy(message = UsMessage(LOST_SIGN_IN_TEXT, UsMessageType.Error))
    }

    private fun TwoFactorUiState.applyError(error: AppError): TwoFactorUiState = when (error) {
        is AppError.AuthFailed ->
            if (isEmailCode) {
                copy(emailNotice = EmailCodeNotice.WrongCode)
            } else {
                copy(codeError = AuthErrorText.forTwoFactor(error))
            }
        is AppError.RateLimited -> copy(
            lockedOut = true,
            message = UsMessage(AuthErrorText.forTwoFactor(error), UsMessageType.Warning),
        )
        else -> copy(message = UsMessage(AuthErrorText.forTwoFactor(error), error.messageType()))
    }

    private fun AppError.messageType(): UsMessageType = when (this) {
        is AppError.NoNetwork, is AppError.Timeout -> UsMessageType.Warning
        else -> UsMessageType.Error
    }

    private fun AppError.code(): String? = when (this) {
        is AppError.Unknown -> code
        is AppError.Server -> code
        is AppError.Forbidden -> code
        else -> null
    }

    private companion object {
        const val LOST_SIGN_IN_TEXT = "We've lost track of this sign-in. Please sign in again."
        const val CODE_METHOD_MISMATCH = "TWO_FACTOR_METHOD_MISMATCH"
        const val CODE_EMAIL_UNAVAILABLE = "EMAIL_UNAVAILABLE"
        const val TICK_MS = 1_000L
    }
}
