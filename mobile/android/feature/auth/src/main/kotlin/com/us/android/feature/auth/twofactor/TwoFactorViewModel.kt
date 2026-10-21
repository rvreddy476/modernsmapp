package com.us.android.feature.auth.twofactor

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthErrorText
import com.us.android.core.auth.AuthRepository
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
) {
    val canSubmit: Boolean
        get() = !isSubmitting && !lockedOut && !signedIn &&
            if (useRecoveryCode) code.isNotBlank() else code.length == CODE_LENGTH

    companion object {
        const val CODE_LENGTH = 6
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

    fun start(userId: String, pendingToken: String) {
        this.userId = userId
        this.pendingToken = pendingToken
    }

    fun onCodeChange(value: String) = _state.update { current ->
        val next = if (current.useRecoveryCode) value.trim() else value.filter(Char::isDigit).take(TwoFactorUiState.CODE_LENGTH)
        current.copy(code = next, codeError = null)
    }

    /** Switches between the six-digit field and the recovery-code field, clearing either. */
    fun toggleRecoveryCode() = _state.update {
        it.copy(useRecoveryCode = !it.useRecoveryCode, code = "", codeError = null, message = null)
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val current = _state.value
        if (!current.canSubmit) return
        if (pendingToken.isBlank() || userId.isBlank()) {
            _state.update { it.copy(message = UsMessage(LOST_SIGN_IN_TEXT, UsMessageType.Error)) }
            return
        }
        _state.update { it.copy(isSubmitting = true, codeError = null, message = null) }
        viewModelScope.launch {
            when (val result = authRepository.verifyTwoFactor(userId, pendingToken, current.code)) {
                is AppResult.Success -> _state.update { it.copy(isSubmitting = false).applySession(result.data) }
                is AppResult.Failure -> _state.update { it.copy(isSubmitting = false, code = "").applyError(result.error) }
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
        is AppError.AuthFailed -> copy(codeError = AuthErrorText.forTwoFactor(error))
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

    private companion object {
        const val LOST_SIGN_IN_TEXT = "We've lost track of this sign-in. Please sign in again."
    }
}
