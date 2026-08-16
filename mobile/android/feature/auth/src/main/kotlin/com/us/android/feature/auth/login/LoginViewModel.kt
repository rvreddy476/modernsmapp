package com.us.android.feature.auth.login

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

data class LoginUiState(
    val identifier: String = "",
    val password: String = "",
    val identifierError: String? = null,
    val passwordError: String? = null,
    /** Transient feedback shown in the shared message host. */
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    /**
     * Set when sign-in reveals the account is unverified.
     *
     * The 403 carries a FRESH verification token, which is the only way a
     * user who closed the app mid-signup can finish. Surfacing it here is
     * what turns that error into a recoverable path instead of a dead end.
     */
    val needsVerificationToken: String? = null,
) {
    val canSubmit: Boolean
        get() = !isSubmitting && identifier.isNotBlank() && password.isNotBlank()
}

@HiltViewModel
class LoginViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(LoginUiState())
    val state: StateFlow<LoginUiState> = _state.asStateFlow()

    fun onIdentifierChange(value: String) =
        _state.update { it.copy(identifier = value, identifierError = null) }

    fun onPasswordChange(value: String) =
        _state.update { it.copy(password = value, passwordError = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val current = _state.value
        if (!current.identifier.contains('@')) {
            _state.value = current.copy(identifierError = "Enter a valid email address")
            return
        }

        _state.value = current.copy(isSubmitting = true, message = null)
        viewModelScope.launch {
            val result = authRepository.login(current.identifier.trim(), current.password)
            _state.update { state ->
                when (result) {
                    // Success does NOT mean signed in. Two of the five
                    // outcomes are HTTP 200 with no tokens, and one is a 403
                    // that is recoverable. Navigation is driven by
                    // SessionState, never from here.
                    is AppResult.Success -> state.copy(
                        isSubmitting = false,
                        message = result.data.pendingMessage(),
                        needsVerificationToken =
                        (result.data as? SessionState.PendingVerification)?.token
                            ?.takeIf { it.isNotBlank() },
                    )
                    is AppResult.Failure -> state.copy(
                        isSubmitting = false,
                        message = UsMessage(
                            text = AuthErrorText.forLogin(result.error),
                            type = result.error.messageType(),
                        ),
                    )
                }
            }
        }
    }

    private fun SessionState.pendingMessage(): UsMessage? = when (this) {
        is SessionState.PendingTwoFactor -> UsMessage(
            "Enter the code from your authenticator app to finish signing in.",
            UsMessageType.Info,
        )
        is SessionState.PendingStepUp -> UsMessage(
            "This sign-in looks unusual. Confirm it via ${methods.humanList()}.",
            UsMessageType.Warning,
        )
        is SessionState.PendingVerification -> UsMessage(
            "Your email isn't verified yet. Check your inbox to finish setting up.",
            UsMessageType.Warning,
        )
        is SessionState.Authenticated -> null
        else -> null
    }

    /** "email or authenticator app", not "[email_otp, totp]". */
    private fun List<String>.humanList(): String {
        val friendly = map { method ->
            when (method) {
                "email_otp" -> "email"
                "totp" -> "your authenticator app"
                "sms_otp" -> "SMS"
                else -> method.replace('_', ' ')
            }
        }
        return when (friendly.size) {
            0 -> "another method"
            1 -> friendly.first()
            else -> friendly.dropLast(1).joinToString(", ") + " or " + friendly.last()
        }
    }

    private fun AppError.messageType(): UsMessageType = when (this) {
        is AppError.NoNetwork, is AppError.Timeout, is AppError.RateLimited ->
            UsMessageType.Warning
        else -> UsMessageType.Error
    }
}
