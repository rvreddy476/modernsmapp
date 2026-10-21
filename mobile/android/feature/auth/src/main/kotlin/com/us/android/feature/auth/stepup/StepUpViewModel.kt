package com.us.android.feature.auth.stepup

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

data class StepUpUiState(
    /** The server's `step_up_methods`, filtered to the two this screen can render. */
    val methods: List<String> = emptyList(),
    /** The channel in use: [AuthRepository.STEP_UP_METHOD_EMAIL] or [AuthRepository.STEP_UP_METHOD_TOTP]. */
    val method: String = "",
    val code: String = "",
    val codeError: String? = null,
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    val signedIn: Boolean = false,
    val lockedOut: Boolean = false,
    /**
     * `STEP_UP_EXPIRED`: the pending session is gone and no code can revive
     * it. The screen sends the person back to sign in rather than letting
     * them keep typing codes at a token the server has already forgotten.
     */
    val expired: Boolean = false,
) {
    val canSubmit: Boolean
        get() = !isSubmitting && !lockedOut && !signedIn && !expired && code.length == CODE_LENGTH
    val canSwitchMethod: Boolean get() = methods.size > 1 && !isSubmitting && !signedIn

    companion object {
        const val CODE_LENGTH = 6
    }
}

/**
 * An unusual sign-in (A13 anomaly step-up).
 *
 * The password matched, but the risk band asked for a second channel. The
 * server lists what it accepts; email is preferred when both are offered
 * because the code is already on its way, the same default the web makes.
 * The pending token is the only handle on the attempt and is never logged.
 */
@HiltViewModel
class StepUpViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(StepUpUiState())
    val state: StateFlow<StepUpUiState> = _state.asStateFlow()

    private var pendingToken: String = ""

    fun start(pendingToken: String, methods: List<String>) {
        this.pendingToken = pendingToken
        val known = methods.filter { it in SUPPORTED_METHODS }
        _state.update {
            it.copy(
                methods = known,
                method = when {
                    AuthRepository.STEP_UP_METHOD_EMAIL in known -> AuthRepository.STEP_UP_METHOD_EMAIL
                    else -> known.firstOrNull().orEmpty()
                },
            )
        }
    }

    fun onCodeChange(value: String) = _state.update {
        it.copy(code = value.filter(Char::isDigit).take(StepUpUiState.CODE_LENGTH), codeError = null)
    }

    fun switchMethod() = _state.update { current ->
        if (!current.canSwitchMethod) return@update current
        val next = current.methods.firstOrNull { it != current.method } ?: current.method
        current.copy(method = next, code = "", codeError = null, message = null)
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val current = _state.value
        if (!current.canSubmit) return
        if (pendingToken.isBlank() || current.method.isBlank()) {
            _state.update { it.copy(expired = true) }
            return
        }
        _state.update { it.copy(isSubmitting = true, codeError = null, message = null) }
        viewModelScope.launch {
            when (val result = authRepository.verifyStepUp(current.method, pendingToken, current.code)) {
                is AppResult.Success -> _state.update { it.copy(isSubmitting = false).applySession(result.data) }
                is AppResult.Failure -> _state.update { it.copy(isSubmitting = false, code = "").applyError(result.error) }
            }
        }
    }

    private fun StepUpUiState.applySession(session: SessionState): StepUpUiState = when (session) {
        is SessionState.Authenticated -> copy(signedIn = true)
        else -> copy(expired = true)
    }

    private fun StepUpUiState.applyError(error: AppError): StepUpUiState = when {
        error is AppError.AuthFailed && error.code == STEP_UP_EXPIRED -> copy(
            expired = true,
            message = UsMessage(AuthErrorText.forStepUp(error), UsMessageType.Warning),
        )
        error is AppError.AuthFailed -> copy(codeError = AuthErrorText.forStepUp(error))
        error is AppError.RateLimited -> copy(
            lockedOut = true,
            message = UsMessage(AuthErrorText.forStepUp(error), UsMessageType.Warning),
        )
        else -> copy(message = UsMessage(AuthErrorText.forStepUp(error), error.messageType()))
    }

    private fun AppError.messageType(): UsMessageType = when (this) {
        is AppError.NoNetwork, is AppError.Timeout -> UsMessageType.Warning
        else -> UsMessageType.Error
    }

    private companion object {
        const val STEP_UP_EXPIRED = "STEP_UP_EXPIRED"
        val SUPPORTED_METHODS = setOf(AuthRepository.STEP_UP_METHOD_EMAIL, AuthRepository.STEP_UP_METHOD_TOTP)
    }
}
