package com.us.android.feature.notifications.permission

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.datastore.SettingsDataStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Owns the "have we asked yet" flag — Slice D, D-D2.
 *
 * The platform state (granted / rationale / SDK level) is read by the screen,
 * which is where the Android APIs live. The one piece of state the platform
 * CANNOT tell us — whether this install has ever shown the prompt — is
 * persisted here. See [NotificationPermissionPolicy] for why that matters.
 */
@HiltViewModel
class NotificationPermissionViewModel @Inject constructor(
    private val settings: SettingsDataStore,
) : ViewModel() {

    private val _hasAsked = MutableStateFlow<Boolean?>(null)

    /**
     * Null until read.
     *
     * Deliberately nullable: acting on a default of `false` before the real
     * value loads would show the prompt to someone who already declined it,
     * which is the one outcome the flag exists to prevent. The screen waits.
     */
    val hasAsked: StateFlow<Boolean?> = _hasAsked.asStateFlow()

    init {
        viewModelScope.launch {
            _hasAsked.value = settings.notificationPermissionAsked.first()
        }
    }

    /**
     * Records that the system prompt was shown.
     *
     * Called when the request is LAUNCHED, not when it returns. A user who
     * dismisses the dialog by tapping outside it produces no result callback,
     * and recording only on the callback would re-prompt them forever.
     */
    fun onPermissionRequested() {
        _hasAsked.value = true
        viewModelScope.launch { settings.setNotificationPermissionAsked() }
    }
}
