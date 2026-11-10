package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.AccountSession
import com.us.android.core.profile.data.AccountSummary
import com.us.android.core.profile.data.SecurityEvent
import com.us.android.core.profile.data.SecuritySettingsRepository
import com.us.android.core.profile.data.TrustedDevice
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface SecuritySettingsUiState {
    data object Loading : SecuritySettingsUiState

    data class Error(val message: String) : SecuritySettingsUiState

    data class Content(
        val account: AccountSummary,
        val sessions: List<AccountSession>,
        val trustedDevices: List<TrustedDevice>,
        val events: List<SecurityEvent>,
        val busy: Boolean = false,
        val message: String? = null,
        val signedOut: Boolean = false,
    ) : SecuritySettingsUiState
}

@HiltViewModel
class SecuritySettingsViewModel @Inject constructor(
    private val settings: SecuritySettingsRepository,
    private val auth: AuthRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<SecuritySettingsUiState>(SecuritySettingsUiState.Loading)
    val state: StateFlow<SecuritySettingsUiState> = _state.asStateFlow()
    init {
        load()
    }

    fun load() {
        _state.value = SecuritySettingsUiState.Loading
        viewModelScope.launch {
            val results = coroutineScope {
                val account = async { settings.account() }
                val sessions = async { settings.sessions() }
                val devices = async { settings.trustedDevices() }
                val events = async { settings.securityEvents() }
                SecurityLoadResults(
                    account.await(),
                    sessions.await(),
                    devices.await(),
                    events.await(),
                )
            }
            _state.value = results.toUiState()
        }
    }

    fun revokeSession(id: String) = runAction { settings.revokeSession(id) }
    fun removeTrustedDevice(id: String) = runAction { settings.removeTrustedDevice(id) }
    fun acknowledge(id: String) = runAction { settings.acknowledgeEvent(id) }

    // Two-step verification lives in TwoFactorSettingsViewModel (emailed
    // codes, founder 10 Nov 2026); this screen hands it the loaded account.

    fun logoutEverywhere() {
        val current = _state.value as? SecuritySettingsUiState.Content ?: return
        if (current.busy) return
        _state.value = current.copy(busy = true, message = null)
        viewModelScope.launch {
            val result = settings.logoutAll()
            if (result is AppResult.Failure) {
                _state.value = current.copy(busy = false, message = "Sessions were not revoked. Please try again.")
                return@launch
            }
            // Clear this device too; a revoked server session with a locally
            // retained access token is a confusing half-logout.
            auth.logout()
            _state.value = current.copy(signedOut = true, busy = false)
        }
    }

    private fun runAction(call: suspend () -> AppResult<Unit>) {
        val current = _state.value as? SecuritySettingsUiState.Content ?: return
        if (current.busy) return
        _state.value = current.copy(busy = true, message = null)
        viewModelScope.launch {
            when (call()) {
                is AppResult.Success -> load()
                is AppResult.Failure -> _state.value = current.copy(
                    busy = false,
                    message = "The security change did not complete.",
                )
            }
        }
    }

    private fun SecurityLoadResults.toUiState(): SecuritySettingsUiState {
        val loadedAccount = (account as? AppResult.Success)?.data
            ?: return SecuritySettingsUiState.Error(LOAD_ERROR)
        val loadedSessions = (sessions as? AppResult.Success)?.data
            ?: return SecuritySettingsUiState.Error(LOAD_ERROR)
        val loadedDevices = (devices as? AppResult.Success)?.data
            ?: return SecuritySettingsUiState.Error(LOAD_ERROR)
        val loadedEvents = (events as? AppResult.Success)?.data
            ?: return SecuritySettingsUiState.Error(LOAD_ERROR)
        return SecuritySettingsUiState.Content(
            loadedAccount,
            loadedSessions,
            loadedDevices,
            loadedEvents,
        )
    }

    private data class SecurityLoadResults(
        val account: AppResult<AccountSummary>,
        val sessions: AppResult<List<AccountSession>>,
        val devices: AppResult<List<TrustedDevice>>,
        val events: AppResult<List<SecurityEvent>>,
    )

    private companion object {
        const val LOAD_ERROR = "Account security details could not be loaded."
    }
}
