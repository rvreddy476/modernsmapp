package com.us.android.screentime

import androidx.lifecycle.ViewModel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/** A thin per-composition handle onto the process-lifetime [ScreenTimeGuardCoordinator]. */
@HiltViewModel
class ScreenTimeGuardViewModel @Inject constructor(
    private val coordinator: ScreenTimeGuardCoordinator,
) : ViewModel() {
    val message: StateFlow<ScreenTimeGuardMessage?> = coordinator.message
    fun dismiss() = coordinator.dismiss()
    fun snooze() = coordinator.snooze()
}
