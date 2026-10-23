package com.us.android.feature.memberships.creator

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.CreatorMember
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.membershipRefusal
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The creator's Members list, as it draws. */
data class CreatorMembersUiState(
    val members: List<CreatorMember> = emptyList(),
    /** The first page is on its way. */
    val loading: Boolean = true,
    /** A later page is on its way. */
    val loadingMore: Boolean = false,
    /** The cursor of the next page; null when the last page is in. */
    val nextCursor: String? = null,
    /** The first page could not be read. */
    val loadError: String? = null,
    /** A later page could not be read: the list stays, with Try again at its foot. */
    val moreError: String? = null,
) {
    val canLoadMore: Boolean get() = nextCursor != null && !loading && !loadingMore && moreError == null
}

/**
 * The signed-in creator's members (`GET /v1/monetization/members`,
 * 2026-10-23), newest first, a page at a time: the next page is asked for
 * with the server's cursor when the list nears its end, a row already shown
 * is never repeated (the subscription id keeps it once), and a failed page
 * keeps what is shown. Opened from the Memberships page and from a
 * new-member notification.
 */
@HiltViewModel
class CreatorMembersViewModel @Inject constructor(
    private val repository: MembershipsRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(CreatorMembersUiState())
    val state: StateFlow<CreatorMembersUiState> = _state.asStateFlow()

    private var inFlight: Job? = null

    init {
        refresh()
    }

    /** The first page again, from the top. */
    fun refresh() {
        inFlight?.cancel()
        _state.update { it.copy(loading = true, loadError = null, moreError = null) }
        inFlight = viewModelScope.launch {
            when (val page = repository.members(cursor = null)) {
                is AppResult.Success -> _state.value = CreatorMembersUiState(
                    members = page.data.items.distinctBy { it.subscriptionId },
                    loading = false,
                    nextCursor = page.data.nextCursor,
                )
                is AppResult.Failure -> _state.update {
                    it.copy(loading = false, loadError = membershipRefusal(page.error, "We couldn't load your members."))
                }
            }
        }
    }

    /** The next page, when there is one and nothing else is loading. */
    fun loadMore() {
        val now = _state.value
        val cursor = now.nextCursor ?: return
        if (!now.canLoadMore) return
        _state.update { it.copy(loadingMore = true) }
        inFlight = viewModelScope.launch {
            when (val page = repository.members(cursor = cursor)) {
                is AppResult.Success -> _state.update { current ->
                    val known = current.members.mapTo(HashSet()) { it.subscriptionId }
                    current.copy(
                        members = current.members + page.data.items.filter { known.add(it.subscriptionId) },
                        loadingMore = false,
                        nextCursor = page.data.nextCursor,
                    )
                }
                is AppResult.Failure -> _state.update {
                    it.copy(loadingMore = false, moreError = membershipRefusal(page.error, "We couldn't load more members."))
                }
            }
        }
    }

    /** Try again after a failed later page. */
    fun retryMore() {
        _state.update { it.copy(moreError = null) }
        loadMore()
    }
}
