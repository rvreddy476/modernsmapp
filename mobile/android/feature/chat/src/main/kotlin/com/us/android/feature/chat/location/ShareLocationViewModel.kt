package com.us.android.feature.chat.location

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.ChatLocation
import com.us.android.core.chat.data.capPlaceText
import com.us.android.core.chat.data.chatLocationOf
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ShareLocationUiState(
    val step: ShareLocationStep = ShareLocationStep.Idle,
    /** The optional name the sender gives the place ("Home"). */
    val label: String = "",
)

/**
 * The "Share location" sheet (founder, 23 Oct 2026): rationale, permission,
 * ONE fused fix, an optional platform-geocoded address, then the confirm
 * card. Sending is the thread's ([com.us.android.feature.chat.ui.ChatThreadViewModel.sendLocation]),
 * through the same durable outbox as every typed message.
 *
 * Nothing here logs or persists the fix; it lives only in this state until
 * the sheet sends or closes.
 */
@HiltViewModel
class ShareLocationViewModel @Inject constructor(
    private val source: ChatLocationSource,
    private val addresses: ChatAddressLookup,
) : ViewModel() {

    private val flow = ShareLocationFlow()
    private val _state = MutableStateFlow(ShareLocationUiState())
    val state: StateFlow<ShareLocationUiState> = _state.asStateFlow()

    private val _effects = Channel<ShareLocationEffect>(Channel.BUFFERED)

    /** One-shot requests the screen acts on — only ever [ShareLocationEffect.RequestPermission]. */
    val effects: Flow<ShareLocationEffect> = _effects.receiveAsFlow()

    private var locating: Job? = null

    /** The sheet opened, or "Try again". */
    fun start() {
        locating?.cancel()
        flow.reset()
        handle(flow.onShareLocation(source.hasPermission()))
    }

    fun onRationaleAccepted() = handle(flow.onRationaleAccepted())

    fun onRationaleDismissed() {
        flow.onRationaleDismissed()
        sync()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) = handle(flow.onPermissionResult(granted, canAskAgain))

    fun onLabel(value: String) = _state.update { it.copy(label = value) }

    /** The location to send, with the typed name; null unless a fix is on screen. */
    fun confirmed(): ChatLocation? {
        val located = flow.step as? ShareLocationStep.Located ?: return null
        return located.location.copy(label = capPlaceText(_state.value.label))
    }

    /** The sheet closed: drop the fix. */
    fun clear() {
        locating?.cancel()
        flow.reset()
        _state.value = ShareLocationUiState()
    }

    private fun handle(effect: ShareLocationEffect) {
        when (effect) {
            ShareLocationEffect.RequestPermission -> _effects.trySend(effect)
            ShareLocationEffect.FetchLocation -> fetch()
            ShareLocationEffect.None -> Unit
        }
        sync()
    }

    private fun fetch() {
        locating?.cancel()
        locating = viewModelScope.launch {
            val fix = source.current()
            val address = fix?.let { addresses.reverse(it) }
            flow.onLocationResult(fix?.let { chatLocationOf(it.latitude, it.longitude, it.accuracyM, label = null, address = address) })
            sync()
        }
    }

    private fun sync() = _state.update { it.copy(step = flow.step) }
}
