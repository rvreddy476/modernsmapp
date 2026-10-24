package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ConnectionPerson
import com.us.android.core.profile.data.ConnectionRequestDirection
import com.us.android.core.profile.data.ConnectionRequestPerson
import com.us.android.core.profile.data.ConnectionsSource
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The tabs of the Connections screen (2026-10-24, the web's `/connections`):
 * an account's connections for everyone, and on the viewer's OWN the two
 * pending lists — requests to them, and the ones they sent.
 */
enum class ConnectionsTab(val wire: String, val label: String) {
    CONNECTIONS("connections", "Connections"),
    RECEIVED("received", "Requests"),
    SENT("sent", "Sent"),
    ;

    companion object {
        fun fromWire(raw: String?): ConnectionsTab = entries.firstOrNull { it.wire == raw } ?: CONNECTIONS

        /** Everyone sees an account's connections; only its owner sees who asked and whom they asked. */
        fun forViewer(isOwn: Boolean): List<ConnectionsTab> = if (isOwn) entries.toList() else listOf(CONNECTIONS)
    }
}

/** One tab's rows. */
@Immutable
data class ConnectionsPage<T>(
    val rows: List<T> = emptyList(),
    val loading: Boolean = false,
    val error: String? = null,
    val loaded: Boolean = false,
) {
    val isEmpty: Boolean get() = loaded && rows.isEmpty() && error == null
}

@Immutable
data class ConnectionsUiState(
    val tabs: List<ConnectionsTab> = listOf(ConnectionsTab.CONNECTIONS),
    val selected: ConnectionsTab = ConnectionsTab.CONNECTIONS,
    val handle: String = "",
    val isOwn: Boolean = false,
    val connections: ConnectionsPage<ConnectionPerson> = ConnectionsPage(),
    val received: ConnectionsPage<ConnectionRequestPerson> = ConnectionsPage(),
    val sent: ConnectionsPage<ConnectionRequestPerson> = ConnectionsPage(),
    /** Rows whose own Accept / Decline / Cancel / Remove is on the wire. */
    val busyIds: Set<String> = emptySet(),
    /** Rows whose last action failed; cleared on the next attempt. */
    val failedIds: Set<String> = emptySet(),
    /** "Remove connection?" is asked about this person. */
    val confirmRemove: ConnectionPerson? = null,
)

/**
 * Behind [ConnectionsScreen]. Accept, Decline, Cancel and Remove are NOT
 * optimistic — the same rule as the follow-request queue and the inbox's
 * request rows: a row shows busy, then leaves its list once the server has
 * the answer, or stays and says it failed. Removing a connection asks first.
 */
@HiltViewModel
class ConnectionsViewModel @Inject constructor(
    private val source: ConnectionsSource,
    sessionStateProvider: SessionStateProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val viewerId: String =
        (sessionStateProvider.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    /** Null or the viewer's own id is the viewer's own screen. */
    private val ownerId: String = savedStateHandle.get<String>(USER_ID_KEY)?.takeIf { it.isNotBlank() } ?: viewerId
    private val isOwn: Boolean = ownerId == viewerId

    private val _state = MutableStateFlow(
        ConnectionsTab.forViewer(isOwn).let { tabs ->
            val asked = ConnectionsTab.fromWire(savedStateHandle.get<String>(TAB_KEY))
            ConnectionsUiState(
                tabs = tabs,
                selected = asked.takeIf { it in tabs } ?: ConnectionsTab.CONNECTIONS,
                handle = savedStateHandle.get<String>(HANDLE_KEY).orEmpty(),
                isOwn = isOwn,
            )
        },
    )
    val state: StateFlow<ConnectionsUiState> = _state.asStateFlow()

    init {
        load(_state.value.selected)
    }

    fun select(tab: ConnectionsTab) {
        if (tab !in _state.value.tabs) return
        _state.update { it.copy(selected = tab) }
        val loaded = when (tab) {
            ConnectionsTab.CONNECTIONS -> _state.value.connections.loaded
            ConnectionsTab.RECEIVED -> _state.value.received.loaded
            ConnectionsTab.SENT -> _state.value.sent.loaded
        }
        if (!loaded) load(tab)
    }

    fun retry() = load(_state.value.selected)

    private fun load(tab: ConnectionsTab) {
        if (ownerId.isBlank()) return
        viewModelScope.launch {
            when (tab) {
                ConnectionsTab.CONNECTIONS -> {
                    _state.update { it.copy(connections = it.connections.copy(loading = true, error = null)) }
                    val result = source.connections(ownerId)
                    _state.update { it.copy(connections = result.toPage(it.connections, MESSAGE_CONNECTIONS)) }
                }
                ConnectionsTab.RECEIVED, ConnectionsTab.SENT -> {
                    val direction = if (tab == ConnectionsTab.RECEIVED) {
                        ConnectionRequestDirection.RECEIVED
                    } else {
                        ConnectionRequestDirection.SENT
                    }
                    setRequests(direction) { it.copy(loading = true, error = null) }
                    val result = source.requests(direction)
                    setRequests(direction) { result.toPage(it, MESSAGE_REQUESTS) }
                }
            }
        }
    }

    /** Accept a request to the viewer: it leaves Requests, and Connections is read again. */
    fun accept(senderId: String) = act(senderId, { source.accept(senderId) }) { state ->
        state.copy(
            received = state.received.copy(rows = state.received.rows.filterNot { it.userId == senderId }),
            // The new connection belongs in the list; it is read again when next opened.
            connections = state.connections.copy(loaded = false),
        )
    }

    fun decline(senderId: String) = act(senderId, { source.decline(senderId) }) { state ->
        state.copy(received = state.received.copy(rows = state.received.rows.filterNot { it.userId == senderId }))
    }

    /** Withdraw a request the viewer sent. */
    fun cancel(receiverId: String) = act(receiverId, { source.cancel(receiverId) }) { state ->
        state.copy(sent = state.sent.copy(rows = state.sent.rows.filterNot { it.userId == receiverId }))
    }

    /** Arms "Remove connection?"; [confirmRemove] is what removes it. Own list only. */
    fun askRemove(person: ConnectionPerson) {
        if (!isOwn) return
        _state.update { it.copy(confirmRemove = person) }
    }

    fun dismissRemove() = _state.update { it.copy(confirmRemove = null) }

    fun confirmRemove() {
        val person = _state.value.confirmRemove ?: return
        _state.update { it.copy(confirmRemove = null) }
        act(person.userId, { source.remove(person.userId) }) { state ->
            state.copy(connections = state.connections.copy(rows = state.connections.rows.filterNot { it.userId == person.userId }))
        }
    }

    /** One row, one action at a time: a double tap must not fire the request twice. */
    private fun act(
        userId: String,
        call: suspend () -> AppResult<Unit>,
        onDone: (ConnectionsUiState) -> ConnectionsUiState,
    ) {
        if (!isOwn || userId in _state.value.busyIds) return
        _state.update { it.copy(busyIds = it.busyIds + userId, failedIds = it.failedIds - userId) }
        viewModelScope.launch {
            val result = call()
            _state.update { state ->
                val settled = state.copy(busyIds = state.busyIds - userId)
                when (result) {
                    is AppResult.Success -> onDone(settled)
                    is AppResult.Failure -> settled.copy(failedIds = settled.failedIds + userId)
                }
            }
        }
    }

    private inline fun setRequests(
        direction: ConnectionRequestDirection,
        transform: (ConnectionsPage<ConnectionRequestPerson>) -> ConnectionsPage<ConnectionRequestPerson>,
    ) = _state.update {
        if (direction == ConnectionRequestDirection.RECEIVED) {
            it.copy(received = transform(it.received))
        } else {
            it.copy(sent = transform(it.sent))
        }
    }

    private companion object {
        const val USER_ID_KEY = "userId"
        const val HANDLE_KEY = "handle"
        const val TAB_KEY = "tab"
        const val MESSAGE_CONNECTIONS = "Couldn't load connections."
        const val MESSAGE_REQUESTS = "Couldn't load connection requests."
    }
}

private fun <T> AppResult<List<T>>.toPage(before: ConnectionsPage<T>, message: String): ConnectionsPage<T> = when (this) {
    is AppResult.Success -> ConnectionsPage(rows = data, loaded = true)
    // Rows already on screen survive a failed re-read; an empty tab says why.
    is AppResult.Failure -> before.copy(loading = false, error = if (before.rows.isEmpty()) message else null)
}
