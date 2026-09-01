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
    val online: Boolean = false,
)

data class FriendsUiState(
    val loading: Boolean = true,
    val friends: List<Friend> = emptyList(),
    /** The friend whose Message tap is in flight; disables just that row. */
    val openingUserId: String? = null,
    val error: String? = null,
    /** The conversation to open. Consumed once, then cleared. */
    val openConversation: OpenFriendConversation? = null,
)

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
                    val name = (profileRepository.getProfile(id) as? AppResult.Success)
                        ?.data?.nameForDisplay
                    Friend(userId = id, displayName = name ?: "Friend")
                }
            }.awaitAll()
            _state.update { it.copy(loading = false, friends = friends) }
            refreshPresence(ids)
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
