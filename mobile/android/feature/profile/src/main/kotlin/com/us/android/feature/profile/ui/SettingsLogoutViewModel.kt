package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The settings page's Log out row (2026-10-17).
 *
 * [confirming] is the "Log out of Momentum on this device?" dialog; nothing
 * signs out until it is confirmed. [signedOut] is the end of the journey:
 * the page then hands over to `:app`'s shared signed-out destination (Login,
 * with the stack cleared), the one sign-out, deactivation and deletion
 * already land on.
 */
data class SettingsLogoutUiState(
    val confirming: Boolean = false,
    val busy: Boolean = false,
    val signedOut: Boolean = false,
)

/**
 * Signs this device out through the app's one sign-out path,
 * [AuthRepository.logout]: the session-teardown tasks (this device's push
 * token, while the token still works), `POST /v1/auth/logout`, then the
 * local session cleared whatever the server answered. Other devices stay
 * signed in; "Log out on every device" is Account and security's.
 */
@HiltViewModel
class SettingsLogoutViewModel internal constructor(
    private val signOut: suspend () -> Unit,
) : ViewModel() {

    @Inject
    constructor(auth: AuthRepository) : this({ auth.logout() })

    private val _state = MutableStateFlow(SettingsLogoutUiState())
    val state: StateFlow<SettingsLogoutUiState> = _state.asStateFlow()

    /** The row was tapped: ask first. */
    fun requestLogout() {
        _state.update { if (it.busy || it.signedOut) it else it.copy(confirming = true) }
    }

    /** "Cancel", or the dialog dismissed: nothing happens. */
    fun cancelLogout() {
        _state.update { if (it.busy) it else it.copy(confirming = false) }
    }

    /** "Log out" in the dialog. Does nothing unless the dialog is up, so a stray call cannot sign anyone out. */
    fun confirmLogout() {
        val current = _state.value
        if (!current.confirming || current.busy || current.signedOut) return
        _state.value = current.copy(busy = true)
        viewModelScope.launch {
            signOut()
            _state.value = SettingsLogoutUiState(signedOut = true)
        }
    }
}
