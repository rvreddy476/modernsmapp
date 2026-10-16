package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.datastore.AppearanceStore
import com.us.android.core.model.AppearanceMode
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The settings hub's Appearance rows (T-32, 2026-10-15): System, Light or
 * Dark, stored on this device. The app's root reads the same store and
 * re-themes the moment a row is picked — there is no Save.
 */
@HiltViewModel
class AppearanceViewModel @Inject constructor(
    private val store: AppearanceStore,
) : ViewModel() {

    val mode: StateFlow<AppearanceMode> = store.mode
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), AppearanceMode.SYSTEM)

    fun select(mode: AppearanceMode) {
        viewModelScope.launch { store.setMode(mode) }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}
