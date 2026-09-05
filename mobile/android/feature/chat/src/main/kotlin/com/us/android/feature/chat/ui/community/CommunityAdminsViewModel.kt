package com.us.android.feature.chat.ui.community

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.CommunityAdmin
import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.PeopleLookupRepository
import com.us.android.core.chat.data.PersonHit
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
)

/**
 * The owner's admin roster: list, add by people search, remove. Every
 * mutation re-reads the roster — what is shown is the server's answer.
 * Admin rows that arrive as bare ids get their names from profiles.
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

    init {
        refresh()
    }

    fun refresh() = viewModelScope.launch {
        when (val result = repository.admins(_state.value.communityId)) {
            is AppResult.Success -> _state.update { it.copy(loading = false, admins = named(result.data)) }
            is AppResult.Failure -> _state.update { it.copy(loading = false, notice = "Couldn't load the admins.") }
        }
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
            _state.update { current ->
                current.copy(
                    searching = false,
                    results = hits.filter { hit -> current.admins.none { it.userId == hit.userId } },
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
            when (action()) {
                is AppResult.Success -> {
                    _state.update { it.copy(results = it.results.filterNot { hit -> hit.userId == userId }) }
                    refresh()
                }
                is AppResult.Failure -> _state.update { it.copy(notice = "That didn't go through. Try again.") }
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
