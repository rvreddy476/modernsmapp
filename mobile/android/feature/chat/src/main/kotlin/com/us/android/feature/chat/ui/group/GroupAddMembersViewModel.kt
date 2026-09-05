package com.us.android.feature.chat.ui.group

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.PeopleLookupRepository
import com.us.android.core.chat.data.PersonHit
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One line of the outcome the server reported for a person. */
data class AddOutcomeLine(val name: String, val text: String)

data class GroupAddMembersUiState(
    val conversationId: String = "",
    val query: String = "",
    val connections: List<PersonHit> = emptyList(),
    val loadingConnections: Boolean = true,
    val results: List<PersonHit> = emptyList(),
    val searching: Boolean = false,
    val selected: Map<String, PersonHit> = emptyMap(),
    /** Ids already in the group; hidden from both lists. */
    val existingIds: Set<String> = emptySet(),
    val adding: Boolean = false,
    val outcomes: List<AddOutcomeLine> = emptyList(),
    val done: Boolean = false,
    val notice: String? = null,
) {
    /** Connections first, then search hits not already offered; the pill filters both. */
    val candidates: List<PersonHit>
        get() {
            val needle = query.trim()
            val fromConnections = connections.filter { hit ->
                needle.isBlank() || hit.nameForDisplay.contains(needle, true) || hit.username.contains(needle, true)
            }
            val fromSearch = results.filter { hit -> fromConnections.none { it.userId == hit.userId } }
            return (fromConnections + fromSearch).filterNot { it.userId in existingIds }
        }
    val canAdd: Boolean get() = selected.isNotEmpty() && !adding
}

/**
 * "Add members": the viewer's connections up front (the graph's list, named
 * through profiles), `v1/search/users` as the pill is typed, multi-select,
 * then one `addMember` per person with the server's honest outcome — added,
 * invited, or skipped by their privacy — read back line by line.
 */
@HiltViewModel
class GroupAddMembersViewModel @Inject constructor(
    private val chat: ChatRepository,
    private val people: PeopleLookupRepository,
    private val profiles: ProfileRepository,
    authRepository: AuthRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val viewerId: String =
        (authRepository.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    private val _state = MutableStateFlow(
        GroupAddMembersUiState(conversationId = savedStateHandle.get<String>("conversationId").orEmpty()),
    )
    val state: StateFlow<GroupAddMembersUiState> = _state.asStateFlow()

    private var searchJob: Job? = null

    init {
        load()
    }

    private fun load() = viewModelScope.launch {
        val existing = async {
            (chat.conversation(_state.value.conversationId) as? AppResult.Success)
                ?.data?.members?.map { it.userId }?.toSet().orEmpty()
        }
        val ids = (chat.connections(viewerId) as? AppResult.Success)?.data.orEmpty()
        val connections = ids.map { id ->
            async {
                val profile = (profiles.getProfile(id) as? AppResult.Success)?.data
                PersonHit(
                    userId = id,
                    username = profile?.username.orEmpty(),
                    displayName = profile?.displayName?.takeIf { it.isNotBlank() } ?: "Connection",
                    avatarUrl = null,
                )
            }
        }.awaitAll()
        _state.update { it.copy(connections = connections, existingIds = existing.await(), loadingConnections = false) }
    }

    fun onQueryChange(query: String) {
        _state.update { it.copy(query = query) }
        searchJob?.cancel()
        if (query.trim().length < MIN_QUERY) {
            _state.update { it.copy(results = emptyList(), searching = false) }
            return
        }
        searchJob = viewModelScope.launch {
            delay(SEARCH_DEBOUNCE_MILLIS)
            _state.update { it.copy(searching = true) }
            val hits = (people.search(query.trim()) as? AppResult.Success)?.data.orEmpty()
            _state.update { it.copy(results = hits.filterNot { hit -> hit.userId == viewerId }, searching = false) }
        }
    }

    fun toggle(hit: PersonHit) = _state.update {
        it.copy(
            selected = if (hit.userId in it.selected) it.selected - hit.userId else it.selected + (hit.userId to hit)
        )
    }

    fun add() {
        val current = _state.value
        if (!current.canAdd) return
        _state.update { it.copy(adding = true, outcomes = emptyList()) }
        viewModelScope.launch {
            val lines = current.selected.values.map { hit ->
                val text = when (val result = chat.addMember(current.conversationId, hit.userId)) {
                    is AppResult.Success -> when (result.data.outcome) {
                        "added" -> "Added"
                        "invited" -> "Invited — joins when they accept"
                        else -> "Skipped — their settings don't allow it"
                    }
                    is AppResult.Failure -> "Couldn't add"
                }
                AddOutcomeLine(hit.nameForDisplay, text)
            }
            _state.update { it.copy(adding = false, outcomes = lines, selected = emptyMap(), done = true) }
        }
    }

    fun dismissNotice() = _state.update { it.copy(notice = null) }

    private companion object {
        const val MIN_QUERY = 2
        const val SEARCH_DEBOUNCE_MILLIS = 300L
    }
}
