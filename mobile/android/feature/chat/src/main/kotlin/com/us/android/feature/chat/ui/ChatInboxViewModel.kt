package com.us.android.feature.chat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.GroupInvitation
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Which inbox surface is selected. */
enum class InboxTab { Chats, Requests, Invites }

/** Everything the inbox renders. */
data class InboxUiState(
    val tab: InboxTab = InboxTab.Chats,
    val conversations: List<Conversation> = emptyList(),
    val requests: List<Conversation> = emptyList(),
    val invitations: List<GroupInvitation> = emptyList(),
    val loading: Boolean = false,
    val syncFailed: Boolean = false,
    /** Needed to name a direct thread after the OTHER participant. */
    val viewerId: String = "",
    /** Row-level busy markers for invitation accept/decline. */
    val busyInvitationIds: Set<String> = emptySet(),
    /** Who is online right now, from the bulk presence endpoint. */
    val onlineUserIds: Set<String> = emptySet(),
    /** The search pill's text; filters the cached rows locally. */
    val query: String = "",
) {
    val requestCount: Int get() = requests.size
    val inviteCount: Int get() = invitations.size

    /** Rows matching the query — the whole list when the pill is empty. */
    val visibleConversations: List<Conversation>
        get() = if (query.isBlank()) {
            conversations
        } else {
            conversations.filter { conversation ->
                conversation.displayTitle(viewerId).contains(query, ignoreCase = true) ||
                    conversation.lastMessagePreview.contains(query, ignoreCase = true)
            }
        }
}

/**
 * The inbox over the DURABLE store (directive §5.4): rows render from Room
 * first — offline, instantly, with unread state that survived process death —
 * and every [refresh] reconciles them against the authoritative lists.
 */
@HiltViewModel
class ChatInboxViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val store: ChatStore,
    private val session: ChatSessionManager,
    authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(
        InboxUiState(
            viewerId = (authRepository.sessionState.value as? SessionState.Authenticated)
                ?.userId.orEmpty(),
        ),
    )
    val state: StateFlow<InboxUiState> = _state.asStateFlow()

    init {
        // One socket per session; the inbox is usually the first chat surface
        // to run, so it arms the manager and the outbox drain.
        session.start()
        store.scheduleDrain()
        observeCache()
        refresh()
    }

    fun selectTab(tab: InboxTab) = _state.update { it.copy(tab = tab) }

    fun onQueryChange(query: String) = _state.update { it.copy(query = query) }

    fun refresh() {
        _state.update { it.copy(loading = true) }
        viewModelScope.launch {
            val ok = store.syncInbox()
            val invitations = repository.invitations()
            _state.update { current ->
                current.copy(
                    loading = false,
                    syncFailed = !ok,
                    invitations = (invitations as? AppResult.Success)?.data
                        ?: current.invitations,
                )
            }
            refreshPresence()
        }
    }

    /**
     * The Online Now rail's data: bulk presence for the OTHER member of every
     * direct conversation. Best-effort — a presence failure costs the rail,
     * never the inbox — and capped at the server's 50-id limit.
     */
    private suspend fun refreshPresence() {
        val viewerId = _state.value.viewerId
        val peers = _state.value.conversations
            .filter { it.type != "group" }
            .flatMap { conversation -> conversation.members.map { it.userId } }
            .filter { it.isNotBlank() && it != viewerId }
            .distinct()
            .take(PRESENCE_LIMIT)
        if (peers.isEmpty()) return
        val online = repository.bulkPresence(peers)
        if (online is AppResult.Success) {
            _state.update { it.copy(onlineUserIds = online.data) }
        }
    }

    /** Flips the pin; the server confirms and the cache reorders the row. */
    fun togglePin(conversation: Conversation) = viewModelScope.launch {
        store.setConversationSettings(
            conversation.id,
            com.us.android.core.chat.data.ConversationSettings(
                isMuted = conversation.isMuted,
                isPinned = !conversation.isPinned,
            ),
        )
    }

    /**
     * Flips the mute, which now reaches the DEVICE: message-service ships the
     * muted recipients on the MessageCreated event and notification-service
     * writes their inbox row without sending a push. Mute silences the phone;
     * it does not hide the conversation or the notification.
     */
    fun toggleMute(conversation: Conversation) = viewModelScope.launch {
        store.setConversationSettings(
            conversation.id,
            com.us.android.core.chat.data.ConversationSettings(
                isMuted = !conversation.isMuted,
                isPinned = conversation.isPinned,
            ),
        )
    }

    fun acceptInvitation(invitationId: String) = resolveInvitation(invitationId) {
        repository.acceptInvitation(invitationId)
    }

    fun declineInvitation(invitationId: String) = resolveInvitation(invitationId) {
        repository.declineInvitation(invitationId)
    }

    private fun resolveInvitation(
        invitationId: String,
        action: suspend () -> AppResult<Unit>,
    ) {
        _state.update { it.copy(busyInvitationIds = it.busyInvitationIds + invitationId) }
        viewModelScope.launch {
            when (action()) {
                is AppResult.Success -> {
                    _state.update { current ->
                        current.copy(
                            invitations = current.invitations.filterNot { it.id == invitationId },
                            busyInvitationIds = current.busyInvitationIds - invitationId,
                        )
                    }
                    // An accepted invitation is a new conversation — resync.
                    store.syncInbox()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(busyInvitationIds = it.busyInvitationIds - invitationId)
                }
            }
        }
    }

    private fun observeCache() {
        viewModelScope.launch {
            store.conversationsFlow().collect { rows ->
                _state.update { it.copy(conversations = rows) }
            }
        }
        viewModelScope.launch {
            store.requestsFlow().collect { rows ->
                _state.update { it.copy(requests = rows) }
            }
        }
    }
}

/** The bulk presence endpoint truncates past 50 ids; don't send more. */
private const val PRESENCE_LIMIT = 50
