package com.us.android.feature.chat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.ConnectionRequest
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One request card: the graph row plus the OTHER party's display identity. */
data class FriendRequestItem(
    /** The other user — sender on Received, receiver on Sent. */
    val userId: String,
    val displayName: String,
    val message: String?,
    /** True when the graph filed this as a suggestion — the design's badge. */
    val suggested: Boolean,
    val createdAt: String,
    /** True while accept/decline/cancel is in flight; disables the card. */
    val busy: Boolean = false,
)

/** Received answers a request; Sent watches (and can withdraw) one. */
enum class RequestsTab(val label: String) { Received("Received"), Sent("Sent") }

data class FriendRequestsUiState(
    val loading: Boolean = true,
    val tab: RequestsTab = RequestsTab.Received,
    val received: List<FriendRequestItem> = emptyList(),
    val sent: List<FriendRequestItem> = emptyList(),
    val error: String? = null,
) {
    val visible: List<FriendRequestItem>
        get() = if (tab == RequestsTab.Received) received else sent

    val emptyText: String
        get() = if (tab == RequestsTab.Received) {
            "No pending requests. When someone asks to be your friend, it lands here."
        } else {
            "Nothing waiting. Requests you send stay here until they're answered."
        }
}

/**
 * The friend-requests surface (Figma 140:104): incoming requests answered
 * with Accept/Decline, outgoing ones listed with Cancel. Every decision goes
 * straight to graph-service; the card disappears only on the server's yes.
 */
@HiltViewModel
class FriendRequestsViewModel @Inject constructor(
    private val repository: ProfileRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(FriendRequestsUiState())
    val state: StateFlow<FriendRequestsUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun selectTab(tab: RequestsTab) = _state.update { it.copy(tab = tab) }

    fun dismissError() = _state.update { it.copy(error = null) }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            val received = repository.pendingConnectionRequests()
            val sent = repository.sentConnectionRequests()
            if (received is AppResult.Failure && sent is AppResult.Failure) {
                _state.update {
                    it.copy(loading = false, error = "Requests couldn't be loaded.")
                }
                return@launch
            }
            _state.update {
                it.copy(
                    loading = false,
                    received = (received as? AppResult.Success)?.data
                        ?.map { r -> r.toItem(otherId = r.senderId) }
                        ?.hydrate() ?: it.received,
                    sent = (sent as? AppResult.Success)?.data
                        ?.map { r -> r.toItem(otherId = r.receiverId) }
                        ?.hydrate() ?: it.sent,
                )
            }
        }
    }

    fun accept(item: FriendRequestItem) = decide(item, RequestsTab.Received) {
        repository.acceptConnectionRequest(item.userId)
    }

    fun decline(item: FriendRequestItem) = decide(item, RequestsTab.Received) {
        repository.declineConnectionRequest(item.userId)
    }

    fun cancel(item: FriendRequestItem) = decide(item, RequestsTab.Sent) {
        repository.cancelConnectionRequest(item.userId)
    }

    /**
     * One decision, one card: the card goes busy, the server answers, and it
     * leaves the list only on success. Optimistically REMOVING it would make
     * a failed accept look accepted — the one lie this screen must not tell.
     */
    private fun decide(
        item: FriendRequestItem,
        tab: RequestsTab,
        action: suspend () -> AppResult<Unit>,
    ) {
        setBusy(tab, item.userId, busy = true)
        viewModelScope.launch {
            when (action()) {
                is AppResult.Success -> _state.update { state ->
                    if (tab == RequestsTab.Received) {
                        state.copy(received = state.received.filterNot { it.userId == item.userId })
                    } else {
                        state.copy(sent = state.sent.filterNot { it.userId == item.userId })
                    }
                }
                is AppResult.Failure -> {
                    setBusy(tab, item.userId, busy = false)
                    _state.update { it.copy(error = "That didn't go through. Try again.") }
                }
            }
        }
    }

    private fun setBusy(tab: RequestsTab, userId: String, busy: Boolean) = _state.update { state ->
        val mark = { list: List<FriendRequestItem> ->
            list.map { if (it.userId == userId) it.copy(busy = busy) else it }
        }
        if (tab == RequestsTab.Received) {
            state.copy(received = mark(state.received))
        } else {
            state.copy(sent = mark(state.sent))
        }
    }

    private fun ConnectionRequest.toItem(otherId: String) = FriendRequestItem(
        userId = otherId,
        displayName = "",
        message = message,
        suggested = source.equals("suggestion", ignoreCase = true),
        createdAt = createdAt,
    )

    /** Names resolved in parallel; an unresolvable profile stays a card. */
    private suspend fun List<FriendRequestItem>.hydrate(): List<FriendRequestItem> =
        kotlinx.coroutines.coroutineScope {
            map { item ->
                async {
                    val profile =
                        (repository.getProfile(item.userId) as? AppResult.Success)?.data
                    item.copy(displayName = profile?.nameForDisplay ?: "Someone")
                }
            }.awaitAll()
        }
}
