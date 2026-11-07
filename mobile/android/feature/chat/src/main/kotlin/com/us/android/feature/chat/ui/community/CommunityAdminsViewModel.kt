package com.us.android.feature.chat.ui.community

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.CommunityAdmin
import com.us.android.core.chat.data.CommunityAdminFailure
import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.PeopleLookupRepository
import com.us.android.core.chat.data.PersonHit
import com.us.android.core.chat.data.toCommunityAdminFailure
import com.us.android.core.common.result.AppResult
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

data class CommunityAdminsUiState(
    val communityId: String = "",
    val loading: Boolean = true,
    val admins: List<CommunityAdmin> = emptyList(),
    val query: String = "",
    val results: List<PersonHit> = emptyList(),
    val searching: Boolean = false,
    val busyUserIds: Set<String> = emptySet(),
    val notice: String? = null,
    /** Why the search offers fewer people than it found: only members can be admins. */
    val searchHint: String? = null,
)

/**
 * The owner's admin roster: list, add by people search, remove. Every
 * mutation re-reads the roster — what is shown is the server's answer.
 * Admin rows that arrive as bare ids get their names from profiles.
 *
 * The search offers only people who can be made admins (7 Nov 2026):
 * members of this community who are not banned and not admins already —
 * channel-service refuses anyone else with a 422. Until the member roster
 * has loaded, the search offers nobody.
 */
@HiltViewModel
class CommunityAdminsViewModel @Inject constructor(
    private val repository: CommunityRepository,
    private val people: PeopleLookupRepository,
    private val profiles: ProfileRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val _state = MutableStateFlow(
        CommunityAdminsUiState(communityId = savedStateHandle.get<String>("communityId").orEmpty()),
    )
    val state: StateFlow<CommunityAdminsUiState> = _state.asStateFlow()

    private var searchJob: Job? = null

    /** Who may be made an admin ([CommunityRepository.adminCandidates]); null until read, or when it failed. */
    private var candidates: Set<String>? = null

    init {
        refresh()
    }

    fun refresh() = viewModelScope.launch {
        val communityId = _state.value.communityId
        val roster = async { repository.adminCandidates(communityId) }
        when (val result = repository.admins(communityId)) {
            is AppResult.Success -> _state.update { it.copy(loading = false, admins = named(result.data)) }
            is AppResult.Failure -> {
                val failure = result.error.toCommunityAdminFailure()
                val notice = if (failure == CommunityAdminFailure.Unavailable) {
                    communityAdminFailureText(failure)
                } else {
                    "Couldn't load the admins."
                }
                _state.update { it.copy(loading = false, notice = notice) }
            }
        }
        candidates = (roster.await() as? AppResult.Success)?.data?.toSet()
    }

    fun onQueryChange(query: String) {
        _state.update { it.copy(query = query) }
        searchJob?.cancel()
        if (query.trim().length < MIN_QUERY) {
            _state.update { it.copy(results = emptyList(), searching = false, searchHint = null) }
            return
        }
        searchJob = viewModelScope.launch {
            delay(SEARCH_DEBOUNCE_MILLIS)
            _state.update { it.copy(searching = true) }
            val hits = (people.search(query.trim()) as? AppResult.Success)?.data.orEmpty()
            // The roster read at open failed or has not landed: one more try.
            val roster = candidates ?: (repository.adminCandidates(_state.value.communityId) as? AppResult.Success)
                ?.data?.toSet()?.also { candidates = it }
            _state.update { current ->
                val offered = adminPickerHits(hits, roster, current.admins)
                current.copy(
                    searching = false,
                    results = offered,
                    searchHint = adminPickerHint(
                        found = hits.size,
                        offered = offered.size,
                        rosterKnown = roster != null,
                    ),
                )
            }
        }
    }

    fun add(userId: String) = mutate(userId) { repository.addAdmin(_state.value.communityId, userId) }

    fun remove(userId: String) = mutate(userId) { repository.removeAdmin(_state.value.communityId, userId) }

    fun dismissNotice() = _state.update { it.copy(notice = null) }

    private fun mutate(userId: String, action: suspend () -> AppResult<Unit>) {
        if (userId in _state.value.busyUserIds) return
        _state.update { it.copy(busyUserIds = it.busyUserIds + userId) }
        viewModelScope.launch {
            when (val result = action()) {
                is AppResult.Success -> {
                    _state.update { it.copy(results = it.results.filterNot { hit -> hit.userId == userId }) }
                    refresh()
                }
                is AppResult.Failure -> {
                    val failure = result.error.toCommunityAdminFailure()
                    // Left, or was banned, since the roster was read: not offered again.
                    val noLongerCandidate = failure == CommunityAdminFailure.NotAMember ||
                        failure == CommunityAdminFailure.Banned
                    if (noLongerCandidate) candidates = candidates?.minus(userId)
                    _state.update { current ->
                        current.copy(
                            notice = communityAdminFailureText(failure),
                            results = current.results.filterNot { noLongerCandidate && it.userId == userId },
                        )
                    }
                }
            }
            _state.update { it.copy(busyUserIds = it.busyUserIds - userId) }
        }
    }

    private suspend fun named(admins: List<CommunityAdmin>): List<CommunityAdmin> = kotlinx.coroutines.coroutineScope {
        admins.map { admin ->
            async {
                if (admin.displayName.isNotBlank()) return@async admin
                val name = (profiles.getProfile(admin.userId) as? AppResult.Success)?.data?.displayName
                admin.copy(displayName = name?.takeIf { it.isNotBlank() } ?: "Admin")
            }
        }.awaitAll()
    }

    private companion object {
        const val MIN_QUERY = 2
        const val SEARCH_DEBOUNCE_MILLIS = 300L
    }
}

/**
 * The people search hits the picker offers: members who may be made admins
 * ([candidates]) and are not admins already. Nobody while the roster is
 * unknown — offering a stranger only earns a 422.
 */
internal fun adminPickerHits(
    hits: List<PersonHit>,
    candidates: Set<String>?,
    admins: List<CommunityAdmin>,
): List<PersonHit> {
    if (candidates == null) return emptyList()
    return hits.filter { hit -> hit.userId in candidates && admins.none { it.userId == hit.userId } }
}

/** Why the picker shows fewer people than the search found; null when it shows them all. */
internal fun adminPickerHint(found: Int, offered: Int, rosterKnown: Boolean): String? = when {
    !rosterKnown && found > 0 -> "Couldn't check who's a member. Try the search again."
    offered < found -> "Only members of this community can be made admins."
    else -> null
}

/** The words for a failed admin change. */
internal fun communityAdminFailureText(failure: CommunityAdminFailure): String = when (failure) {
    CommunityAdminFailure.NotAMember -> "Only members can be admins. They need to join the community first."
    CommunityAdminFailure.Banned -> "They're banned from this community. Unban them before making them an admin."
    CommunityAdminFailure.NotAllowed -> "Only the community's owner can change its admins."
    CommunityAdminFailure.Unavailable -> COMMUNITY_UNAVAILABLE_TEXT
    CommunityAdminFailure.Other -> "That didn't go through. Try again."
}

/** A community the viewer may not see: deleted, suspended, or private and they are not a member. */
internal const val COMMUNITY_UNAVAILABLE_TEXT =
    "This community isn't available. It may have been removed, or it's private and you're not a member."
