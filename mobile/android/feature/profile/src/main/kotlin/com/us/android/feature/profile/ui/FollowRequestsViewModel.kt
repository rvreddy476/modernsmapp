package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowRequest
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One requester row, as the screen renders it. */
@Immutable
data class FollowRequestRow(
    val requesterId: String,
    val createdAt: String,
    /**
     * Resolved separately via [ProfileRepository.getProfile] — the incoming-
     * requests endpoint carries only the id. Empty while that lookup is still
     * in flight or failed, and the row falls back to "Someone" the same way a
     * notification row does when its actor hydration comes back empty.
     */
    val displayName: String = "",
    /** True while this row's own Accept/Decline call is in flight. */
    val busy: Boolean = false,
    /** Set when this row's last action failed; cleared on the next attempt. */
    val actionFailed: Boolean = false,
)

@Immutable
data class FollowRequestsUiState(
    val rows: List<FollowRequestRow> = emptyList(),
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    val error: String? = null,
    val nextCursor: String? = null,
) {
    val isEmpty: Boolean get() = rows.isEmpty() && !loading && error == null
    val hasMore: Boolean get() = nextCursor != null
}

/**
 * Drives the incoming follow-requests screen — the owner-side half of private
 * accounts.
 *
 * Accept/Decline are NOT optimistic, the same rule the notification inbox's
 * message-request row follows: "you are now connected to a stranger" is not a
 * state worth guessing at. A row shows busy, then either disappears (accepted
 * or declined) or reports failure and stays, so nothing the user asked for is
 * silently dropped.
 */
@HiltViewModel
class FollowRequestsViewModel @Inject constructor(
    private val repository: ProfileRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(FollowRequestsUiState())
    val state: StateFlow<FollowRequestsUiState> = _state.asStateFlow()

    /** The one in-flight page request, mirroring the notification inbox's rule. */
    private var pageJob: Job? = null

    init {
        refresh()
    }

    fun refresh() {
        if (pageJob?.isActive == true) return
        _state.value = _state.value.copy(loading = _state.value.rows.isEmpty(), error = null)

        pageJob = viewModelScope.launch {
            when (val result = repository.incomingFollowRequests()) {
                is AppResult.Success -> {
                    _state.value = _state.value.copy(
                        rows = result.data.items.map { it.toRow() },
                        nextCursor = result.data.nextCursor,
                        loading = false,
                        error = null,
                    )
                    hydrateNames()
                }

                is AppResult.Failure -> _state.value = _state.value.copy(
                    loading = false,
                    error = if (_state.value.rows.isEmpty()) MESSAGE_LOAD_FAILED else _state.value.error,
                )
            }
        }
    }

    fun loadMore() {
        val cursor = _state.value.nextCursor ?: return
        if (_state.value.loadingMore || pageJob?.isActive == true) return
        _state.value = _state.value.copy(loadingMore = true)

        pageJob = viewModelScope.launch {
            when (val result = repository.incomingFollowRequests(cursor = cursor)) {
                is AppResult.Success -> {
                    val known = _state.value.rows.mapTo(mutableSetOf()) { it.requesterId }
                    val fresh = result.data.items.filterNot { it.requesterId in known }.map { it.toRow() }
                    _state.value = _state.value.copy(
                        rows = _state.value.rows + fresh,
                        nextCursor = result.data.nextCursor,
                        loadingMore = false,
                    )
                    hydrateNames()
                }

                // The loaded rows survive; retry re-issues with the same cursor.
                is AppResult.Failure -> _state.value = _state.value.copy(loadingMore = false)
            }
        }
    }

    fun accept(requesterId: String) = act(requesterId) { repository.acceptFollowRequest(requesterId) }

    fun decline(requesterId: String) = act(requesterId) { repository.declineFollowRequest(requesterId) }

    /** One row, one action at a time: a double-tap must not fire the request twice. */
    private fun act(requesterId: String, call: suspend () -> AppResult<Unit>) {
        if (_state.value.rows.firstOrNull { it.requesterId == requesterId }?.busy == true) return
        setRow(requesterId) { it.copy(busy = true, actionFailed = false) }

        viewModelScope.launch {
            when (call()) {
                // Accepted or declined: the request is resolved either way, so
                // the row leaves the list rather than showing an outcome label
                // — there is nothing further to act on here, unlike a
                // notification row the user might still want to see.
                is AppResult.Success -> _state.value = _state.value.copy(
                    rows = _state.value.rows.filterNot { it.requesterId == requesterId },
                )

                is AppResult.Failure -> setRow(requesterId) { it.copy(busy = false, actionFailed = true) }
            }
        }
    }

    private inline fun setRow(requesterId: String, transform: (FollowRequestRow) -> FollowRequestRow) {
        _state.value = _state.value.copy(
            rows = _state.value.rows.map { if (it.requesterId == requesterId) transform(it) else it },
        )
    }

    /**
     * Resolves each requester's name through the per-row profile lookup — no
     * batch endpoint exists in `:core:profile` yet. Bounded and concurrent,
     * the same shape the notification inbox uses to check "already following"
     * for a page of follow notifications.
     */
    private fun hydrateNames() {
        val targets = _state.value.rows.filter { it.displayName.isBlank() }.map { it.requesterId }
        if (targets.isEmpty()) return
        viewModelScope.launch {
            val resolved = coroutineScope {
                targets.map { id -> async { id to repository.getProfile(id) } }.awaitAll()
            }
            for ((id, result) in resolved) {
                if (result is AppResult.Success) {
                    setRow(id) { it.copy(displayName = result.data.nameForDisplay) }
                }
            }
        }
    }

    private companion object {
        const val MESSAGE_LOAD_FAILED = "Couldn't load your follow requests."
    }
}

private fun FollowRequest.toRow() = FollowRequestRow(requesterId = requesterId, createdAt = createdAt)
