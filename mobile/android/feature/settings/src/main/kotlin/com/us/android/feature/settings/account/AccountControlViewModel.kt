package com.us.android.feature.settings.account

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

enum class AccountControlAction { DEACTIVATE, DELETE }

data class AccountControlUiState(
    /** Which card's confirmation sheet is open, or null when neither is. */
    val activeSheet: AccountControlAction? = null,
    val password: String = "",
    val submitting: Boolean = false,
    val error: String? = null,
    /** True once the server has revoked the session; the screen navigates to Login on this. */
    val signedOut: Boolean = false,
)

/**
 * Deactivate / delete. Both calls live on [AuthRepository] so the local
 * session is cleared by the exact same code sign-out uses — see
 * `AuthRepository.deactivateAccount` / `deleteAccount`.
 */
@HiltViewModel
class AccountControlViewModel @Inject constructor(
    private val authRepository: AuthRepository,
) : ViewModel() {
    private val _state = MutableStateFlow(AccountControlUiState())
    val state: StateFlow<AccountControlUiState> = _state.asStateFlow()

    fun openSheet(action: AccountControlAction) {
        _state.value = AccountControlUiState(activeSheet = action)
    }

    fun dismissSheet() {
        if (_state.value.submitting) return
        _state.value = AccountControlUiState()
    }

    fun setPassword(value: String) = _state.update { it.copy(password = value, error = null) }

    fun confirm() {
        val current = _state.value
        val action = current.activeSheet ?: return
        if (current.submitting || current.password.isBlank()) return
        _state.value = current.copy(submitting = true, error = null)
        viewModelScope.launch {
            val result = when (action) {
                AccountControlAction.DEACTIVATE -> authRepository.deactivateAccount(current.password).map { }
                AccountControlAction.DELETE -> authRepository.deleteAccount(current.password).map { }
            }
            _state.value = when (result) {
                is AppResult.Success -> current.copy(submitting = false, signedOut = true)
                is AppResult.Failure -> current.copy(submitting = false, error = result.error.toMessage())
            }
        }
    }

    private fun AppError.toMessage(): String = when (this) {
        is AppError.AuthFailed -> "Incorrect password."
        is AppError.Unknown -> if (code == "ACCOUNT_STATE_CONFLICT") {
            "Your account is already in that state. Try signing in again to see its current status."
        } else {
            "Something went wrong. Please try again."
        }
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        else -> "Something went wrong. Please try again."
    }
}
