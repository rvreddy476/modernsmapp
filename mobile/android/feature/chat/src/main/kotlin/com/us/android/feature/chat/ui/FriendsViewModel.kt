package com.us.android.feature.chat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.StartDirectController
import com.us.android.core.chat.data.StartDirectResult
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
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

/** One accepted connection, ready to message. */
data class Friend(
    val userId: String,
    val displayName: String,
    val username: String = "",
    val online: Boolean = false,
)

/** The three lenses of the Figma friends frame (140:199). */
enum class FriendsTab(val label: String) {
    All("All"),
    Close("Close Friends"),
    Recent("Recently Added"),
}

data class FriendsUiState(
    val loading: Boolean = true,
    val friends: List<Friend> = emptyList(),
    val tab: FriendsTab = FriendsTab.All,
    val query: String = "",
    /** Ids the graph marks close friends — the second tab's membership. */
    val closeFriendIds: Set<String> = emptySet(),
    /** Pending incoming friend requests — the header badge count. */
    val pendingRequestCount: Int = 0,
    /** The friend whose Message tap is in flight; disables just that row. */
    val openingUserId: String? = null,
    val error: String? = null,
    /** The conversation to open. Consumed once, then cleared. */
    val openConversation: OpenFriendConversation? = null,
) {
    /**
     * The rows the selected tab shows. Recently Added leans on the server
     * ordering — `GET /connections` returns newest-first — and takes its
     * head, because connection DATES are not exposed to cut by time.
     */
    val visibleFriends: List<Friend>
        get() {
            val byTab = when (tab) {
                FriendsTab.All -> friends
                FriendsTab.Close -> friends.filter { it.userId in closeFriendIds }
                FriendsTab.Recent -> friends.take(RECENT_LIMIT)
            }
            if (query.isBlank()) return byTab
            return byTab.filter {
                it.displayName.contains(query, ignoreCase = true) ||
                    it.username.contains(query, ignoreCase = true)
            }
        }

    private companion object {
        const val RECENT_LIMIT = 10
    }
}

/** A conversation the host should navigate to. */
data class OpenFriendConversation(val conversationId: String, val title: String)

/**
 * The Friends surface: the viewer's ACCEPTED connections (graph authority),
 * names resolved through profiles, each row one tap from a direct thread.
 *
 * The list is the server's answer, not a local cache of past taps — the same
 * `GET /v1/graph/connections` the group member picker uses, so both surfaces
 * always agree on who a friend is.
 */
@HiltViewModel
class FriendsViewModel @Inject constructor(
    private val chatRepository: ChatRepository,
    private val profileRepository: ProfileRepository,
    authRepository: AuthRepository,
) : ViewModel() {

    private val viewerId: String =
        (authRepository.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    /** One intent, one key — retries reuse it; see [StartDirectController]. */
    private val controller = StartDirectController(chatRepository)

    private val _state = MutableStateFlow(FriendsUiState())
    val state: StateFlow<FriendsUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            val ids = when (val result = chatRepository.connections(viewerId)) {
                is AppResult.Success -> result.data
                is AppResult.Failure -> {
                    _state.update {
                        it.copy(loading = false, error = "Your friends couldn't be loaded.")
                    }
                    return@launch
                }
            }
            // Names in parallel; a profile that fails to resolve still shows
            // as a row with a neutral name rather than disappearing.
            val friends = ids.map { id ->
                async {
                    val profile = (profileRepository.getProfile(id) as? AppResult.Success)?.data
                    Friend(
                        userId = id,
                        displayName = profile?.nameForDisplay ?: "Friend",
                        username = profile?.username.orEmpty(),
                    )
                }
            }.awaitAll()
            _state.update { it.copy(loading = false, friends = friends) }
            refreshPresence(ids)
            refreshEdges()
        }
    }

    fun selectTab(tab: FriendsTab) = _state.update { it.copy(tab = tab) }

    fun onQueryChange(value: String) = _state.update { it.copy(query = value) }

    /**
     * Close-friend membership and the pending-request badge, best-effort —
     * a graph blip degrades the tab to empty and the badge to zero, never
     * to an error screen over a loaded list.
     */
    private suspend fun refreshEdges() {
        (profileRepository.closeFriends() as? AppResult.Success)?.data?.let { ids ->
            _state.update { it.copy(closeFriendIds = ids.toSet()) }
        }
        (profileRepository.pendingConnectionRequests() as? AppResult.Success)?.data?.let { reqs ->
            _state.update { it.copy(pendingRequestCount = reqs.size) }
        }
    }

    /** Opens (or reopens) the direct thread with [friend]. */
    fun message(friend: Friend) {
        if (_state.value.openingUserId != null) return
        _state.update { it.copy(openingUserId = friend.userId, error = null) }
        viewModelScope.launch {
            val next = when (val result = controller.open(friend.userId)) {
                is StartDirectResult.Opened -> _state.value.copy(
                    openingUserId = null,
                    openConversation = OpenFriendConversation(
                        conversationId = result.conversation.id,
                        title = friend.displayName,
                    ),
                )
                is StartDirectResult.NotAllowed -> _state.value.copy(
                    openingUserId = null,
                    error = "${friend.displayName} isn't accepting messages from you.",
                )
                is StartDirectResult.Failed -> _state.value.copy(
                    openingUserId = null,
                    error = "That conversation couldn't be opened.",
                )
            }
            _state.value = next
        }
    }

    /** Clears the navigation request after the host has acted on it. */
    fun onConversationOpened() = _state.update { it.copy(openConversation = null) }

    fun dismissError() = _state.update { it.copy(error = null) }

    /** Best-effort green dots — a presence blip must not cost the list. */
    private suspend fun refreshPresence(ids: List<String>) {
        if (ids.isEmpty()) return
        val presence = chatRepository.bulkPresence(ids.take(PRESENCE_LIMIT))
        val online = (presence as? AppResult.Success)?.data ?: return
        _state.update { state ->
            state.copy(friends = state.friends.map { it.copy(online = it.userId in online) })
        }
    }

    private companion object {
        /** The presence endpoint caps a lookup at 50 ids. */
        const val PRESENCE_LIMIT = 50
    }
}
