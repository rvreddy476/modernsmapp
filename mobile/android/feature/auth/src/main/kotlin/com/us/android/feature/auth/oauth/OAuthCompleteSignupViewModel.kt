package com.us.android.feature.auth.oauth

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
import java.util.UUID
import javax.inject.Inject

enum class OAuthSignupStep { Phone, Code }

data class OAuthCompleteSignupUiState(
    val step: OAuthSignupStep = OAuthSignupStep.Phone,
    /** "Google", for the copy; from the server's `provider`. */
    val providerLabel: String = "",
    val email: String = "",
    /** The ten digits the person typed; `+91` is implied, as on the partner sign-in. */
    val phone: String = "",
    val phoneError: String? = null,
    val code: String = "",
    val codeError: String? = null,
    val message: UsMessage? = null,
    val isSubmitting: Boolean = false,
    val signedIn: Boolean = false,
    val lockedOut: Boolean = false,
) {
    val canSendCode: Boolean get() = !isSubmitting && !lockedOut && phone.length == PHONE_DIGITS
    val canVerify: Boolean get() = !isSubmitting && !lockedOut && !signedIn && code.length == CODE_LENGTH

    companion object {
        const val PHONE_DIGITS = 10
        const val CODE_LENGTH = 6
    }
}

/**
 * The A5 "finish signing up" step after a Google sign-in whose email the
 * provider did not vouch for: a phone number, then its code, then the account
 * exists and a session is issued (201). Mirrors the web's
 * `/auth/oauth/complete-signup`.
 *
 * The pending token is the only handle on the half-made account; it is a
 * navigation argument, never logged. The phone's country code is fixed to
 * India, as the partner sign-in fixes it, because that is where the product
 * launches and where its SMS sender would be registered.
 */
@HiltViewModel
class OAuthCompleteSignupViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(OAuthCompleteSignupUiState())
    val state: StateFlow<OAuthCompleteSignupUiState> = _state.asStateFlow()

    private var pendingToken: String = ""
    private val deviceId = UUID.randomUUID().toString()

    fun start(pendingToken: String, provider: String, email: String) {
        this.pendingToken = pendingToken
        _state.update {
            it.copy(
                providerLabel = provider.replaceFirstChar(Char::uppercaseChar).ifBlank { "Your provider" },
                email = email,
            )
        }
    }

    fun onPhoneChange(value: String) = _state.update {
        it.copy(phone = value.filter(Char::isDigit).take(OAuthCompleteSignupUiState.PHONE_DIGITS), phoneError = null)
    }

    fun onCodeChange(value: String) = _state.update {
        it.copy(code = value.filter(Char::isDigit).take(OAuthCompleteSignupUiState.CODE_LENGTH), codeError = null)
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Back to the number, keeping it; the server re-sends to whatever is submitted next. */
    fun changePhone() = _state.update {
        if (it.isSubmitting) it else it.copy(step = OAuthSignupStep.Phone, code = "", codeError = null, message = null)
    }

    fun sendCode() {
        val current = _state.value
        if (!current.canSendCode) return
        if (pendingToken.isBlank()) {
            _state.update { it.copy(message = UsMessage(LOST_SIGNUP_TEXT, UsMessageType.Error)) }
            return
        }
        if (!current.phone.first().isIndianMobileLead()) {
            _state.update { it.copy(phoneError = "Enter a valid 10-digit mobile number.") }
            return
        }
        _state.update { it.copy(isSubmitting = true, phoneError = null, message = null) }
        viewModelScope.launch {
            when (val result = authRepository.completeOAuthSignup(pendingToken, "+91${current.phone}")) {
                is AppResult.Success -> _state.update {
                    it.copy(isSubmitting = false, step = OAuthSignupStep.Code, code = "")
                }
                is AppResult.Failure -> _state.update { it.copy(isSubmitting = false).applyPhoneError(result.error) }
            }
        }
    }

    fun verify() {
        val current = _state.value
        if (!current.canVerify) return
        if (pendingToken.isBlank()) {
            _state.update { it.copy(message = UsMessage(LOST_SIGNUP_TEXT, UsMessageType.Error)) }
            return
        }
        _state.update { it.copy(isSubmitting = true, codeError = null, message = null) }
        viewModelScope.launch {
            when (val result = authRepository.verifyOAuthSignup(pendingToken, current.code, deviceId)) {
                is AppResult.Success -> _state.update { it.copy(isSubmitting = false).applySession(result.data) }
                is AppResult.Failure -> _state.update { it.copy(isSubmitting = false, code = "").applyCodeError(result.error) }
            }
        }
    }

    private fun OAuthCompleteSignupUiState.applySession(session: SessionState) = when (session) {
        is SessionState.Authenticated -> copy(signedIn = true)
        else -> copy(message = UsMessage(LOST_SIGNUP_TEXT, UsMessageType.Error))
    }

    private fun OAuthCompleteSignupUiState.applyPhoneError(error: AppError) = when (error) {
        is AppError.Unknown, is AppError.InvalidRequest -> copy(phoneError = AuthErrorText.forOAuth(error))
        else -> applyError(error)
    }

    private fun OAuthCompleteSignupUiState.applyCodeError(error: AppError) = when (error) {
        is AppError.AuthFailed -> copy(codeError = AuthErrorText.forOAuth(error))
        else -> applyError(error)
    }

    private fun OAuthCompleteSignupUiState.applyError(error: AppError) = when (error) {
        is AppError.RateLimited -> copy(
            lockedOut = true,
            message = UsMessage(AuthErrorText.forOAuth(error), UsMessageType.Warning),
        )
        is AppError.NoNetwork, is AppError.Timeout ->
            copy(message = UsMessage(AuthErrorText.forOAuth(error), UsMessageType.Warning))
        else -> copy(message = UsMessage(AuthErrorText.forOAuth(error), UsMessageType.Error))
    }

    private fun Char.isIndianMobileLead(): Boolean = this in '6'..'9'

    private companion object {
        const val LOST_SIGNUP_TEXT = "We've lost track of this sign-up. Please sign in with Google again."
    }
}
