package com.us.android.feature.call.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.call.CallSessionManager
import com.us.android.core.call.CallState
import com.us.android.core.call.data.CallHistoryItem
import com.us.android.core.call.data.CallRepository
import com.us.android.core.common.result.AppResult
import com.us.android.feature.call.navigation.CallRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Thin adapter over [CallSessionManager] — the machine owns every rule; this
 * ViewModel only carries route context (who to ring) and user gestures.
 */
@HiltViewModel
class CallViewModel @Inject constructor(
    val manager: CallSessionManager,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val route: CallRoute = savedStateHandle.toRoute()

    val callState: StateFlow<CallState> = manager.state

    /** Display-only: the name the route carried (outgoing) — never authority. */
    val peerName: String = route.peerName

    val outgoingVideo: Boolean = route.video

    private val _message = MutableStateFlow<String?>(null)
    val message: StateFlow<String?> = _message.asStateFlow()

    private var started = false

    /**
     * Called once mic (and camera, for video) permissions are granted.
     * Outgoing mode rings; attach mode re-checks the server for a pending
     * invite so a cold start from a push still finds the ring.
     */
    fun onReady() {
        if (started) return
        started = true
        val peerId = route.peerId
        viewModelScope.launch {
            if (peerId != null) {
                manager.startOutgoing(peerId, route.video, route.conversationId)
                    ?.let { refusal -> _message.value = refusal }
            } else if (manager.state.value is CallState.Idle) {
                manager.refreshIncoming()
            }
        }
    }

    fun onPermissionsDenied() {
        _message.value = "Calls need microphone access."
        viewModelScope.launch {
            if (manager.state.value is CallState.Incoming) manager.declineIncoming()
        }
    }

    /**
     * CALL-LB-6: the surface passes the CURRENT camera grant; the manager
     * refuses a video accept without it (the call keeps ringing so the user
     * can grant and retry). Audio calls ignore the flag.
     */
    fun accept(cameraGranted: Boolean) = viewModelScope.launch {
        manager.acceptIncoming(cameraGranted)?.let { _message.value = it }
    }

    /**
     * CALL-LB-6: camera denied on a video Accept. The call STAYS Incoming —
     * Accept remains the retry action and a Settings grant can still answer
     * it — so this only surfaces the reason.
     */
    fun onCameraDenied() {
        _message.value = "Video calls need camera access."
    }

    /**
     * CALL-LB-6: microphone denied on an Accept tap. Same contract as the
     * camera arm: the call stays Incoming for a retry or Settings grant —
     * never declined by a permission dialog.
     */
    fun onMicDenied() {
        _message.value = "Calls need microphone access."
    }

    fun decline() = viewModelScope.launch { manager.declineIncoming() }

    fun hangUp() = viewModelScope.launch { manager.hangUp() }

    fun toggleMute() = manager.toggleMute()
    fun toggleVideo() = manager.toggleVideo()
    fun toggleSpeaker() = manager.toggleSpeaker()
    fun switchCamera() = manager.switchCamera()

    fun dismissEnded() = manager.reset()
}

@HiltViewModel
class CallHistoryViewModel @Inject constructor(
    private val repository: CallRepository,
) : ViewModel() {

    private val _items = MutableStateFlow<List<CallHistoryItem>>(emptyList())
    val items: StateFlow<List<CallHistoryItem>> = _items.asStateFlow()

    private val _loading = MutableStateFlow(true)
    val loading: StateFlow<Boolean> = _loading.asStateFlow()

    init {
        refresh()
    }

    fun refresh() = viewModelScope.launch {
        _loading.value = true
        when (val result = repository.history()) {
            is AppResult.Success -> _items.value = result.data
            is AppResult.Failure -> Unit // pull-to-refresh path; list stays
        }
        _loading.value = false
    }
}
