package com.us.android.feature.tube.ui.credits

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.credits.CreditRequest
import com.us.android.feature.tube.data.CreditRequestsSource
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One row of the screen: the request, and whether the viewer has accepted it this visit. */
data class CreditRequestRow(val request: CreditRequest, val accepted: Boolean = false)

/** The credit requests screen, as it draws. */
data class CreditRequestsUiState(
    /** The first page is on its way and nothing is shown yet: the skeleton. */
    val loading: Boolean = true,
    /** The first page failed: the error with Retry. */
    val loadError: String? = null,
    val rows: List<CreditRequestRow> = emptyList(),
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    /** A pull to refresh is running over rows already shown. */
    val refreshing: Boolean = false,
    /** Rows with an answer in flight, so a second tap does nothing. */
    val busy: Set<String> = emptySet(),
    /** A refused answer, said once; [CreditRequestsViewModel.messageShown] clears it. */
    val message: String? = null,
) {
    val empty: Boolean get() = !loading && loadError == null && rows.isEmpty()
    val canLoadMore: Boolean get() = nextCursor != null && !loadingMore
}

/**
 * The viewer's pending credit requests (video credits, 2026-10-23): someone
 * credited them in a long video and the tag waits for their answer.
 *
 * Accept and Decline are OPTIMISTIC: the row moves at once and moves back,
 * with a message, if the server refuses. An accepted row stays on screen,
 * marked, with "Remove me from this video" — the one place to take it back
 * right after saying yes. A declined row leaves (the credit stays on the
 * video as plain text). A 404 means the request is gone (the uploader
 * removed the credit): the row leaves rather than coming back.
 */
@HiltViewModel
class CreditRequestsViewModel @Inject constructor(
    private val source: CreditRequestsSource,
) : ViewModel() {

    private val _state = MutableStateFlow(CreditRequestsUiState())
    val state: StateFlow<CreditRequestsUiState> = _state.asStateFlow()

    init {
        load(refreshing = false)
    }

    fun retry() = load(refreshing = false)

    fun refresh() = load(refreshing = true)

    private fun load(refreshing: Boolean) {
        _state.update {
            if (refreshing) it.copy(refreshing = true) else it.copy(loading = it.rows.isEmpty(), loadError = null)
        }
        viewModelScope.launch {
            when (val result = source.requests(cursor = null)) {
                is AppResult.Success -> _state.update { current ->
                    // Rows accepted this visit stay, so "Remove me" survives a pull.
                    val kept = current.rows.filter { it.accepted && result.data.items.none { r -> r.id == it.request.id } }
                    current.copy(
                        loading = false,
                        refreshing = false,
                        loadError = null,
                        rows = kept + result.data.items.map { CreditRequestRow(it) },
                        nextCursor = result.data.nextCursor,
                    )
                }
                is AppResult.Failure -> _state.update { current ->
                    if (current.rows.isEmpty()) {
                        current.copy(loading = false, refreshing = false, loadError = loadMessage(result.error))
                    } else {
                        current.copy(refreshing = false, message = loadMessage(result.error))
                    }
                }
            }
        }
    }

    /** The next page, when there is one; asked for as the list nears its end. */
    fun loadMore() {
        val now = _state.value
        val cursor = now.nextCursor ?: return
        if (now.loadingMore) return
        _state.update { it.copy(loadingMore = true) }
        viewModelScope.launch {
            when (val result = source.requests(cursor)) {
                is AppResult.Success -> _state.update { current ->
                    val known = current.rows.map { it.request.id }.toSet()
                    current.copy(
                        loadingMore = false,
                        rows = current.rows + result.data.items.filter { it.id !in known }.map { CreditRequestRow(it) },
                        nextCursor = result.data.nextCursor,
                    )
                }
                is AppResult.Failure ->
                    _state.update { it.copy(loadingMore = false, message = loadMessage(result.error)) }
            }
        }
    }

    /** Accept: the row is marked at once; refused, it goes back to pending. */
    fun accept(id: String) = answer(
        id = id,
        apply = { rows -> rows.map { if (it.request.id == id) it.copy(accepted = true) else it } },
        call = { source.accept(id) },
        refused = "Couldn't accept that credit. Try again.",
    )

    /** Decline: the row leaves at once; refused, it comes back where it was. */
    fun decline(id: String) = answer(
        id = id,
        apply = { rows -> rows.filterNot { it.request.id == id } },
        call = { source.decline(id) },
        refused = "Couldn't decline that credit. Try again.",
    )

    /** "Remove me from this video" on an accepted row: it leaves; refused, it comes back accepted. */
    fun removeMe(id: String) = answer(
        id = id,
        apply = { rows -> rows.filterNot { it.request.id == id } },
        call = { source.removeMe(id) },
        refused = "Couldn't remove you from that video. Try again.",
    )

    fun messageShown() = _state.update { it.copy(message = null) }

    private fun answer(
        id: String,
        apply: (List<CreditRequestRow>) -> List<CreditRequestRow>,
        call: suspend () -> AppResult<Unit>,
        refused: String,
    ) {
        val before = _state.value
        if (id in before.busy || before.rows.none { it.request.id == id }) return
        val snapshot = before.rows
        _state.update { it.copy(rows = apply(it.rows), busy = it.busy + id, message = null) }
        viewModelScope.launch {
            val result = call()
            _state.update { current ->
                when {
                    result is AppResult.Success -> current.copy(busy = current.busy - id)
                    // Gone on the server (the credit was removed): nothing to roll back to.
                    (result as AppResult.Failure).error is AppError.NotFound -> current.copy(
                        busy = current.busy - id,
                        rows = current.rows.filterNot { it.request.id == id },
                        message = "That credit is no longer there.",
                    )
                    else -> current.copy(busy = current.busy - id, rows = rollback(current.rows, snapshot, id), message = refused)
                }
            }
        }
    }

    private fun loadMessage(error: AppError): String = when (error) {
        is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Check your connection."
        else -> "We couldn't load your credit requests."
    }

    companion object {
        /**
         * [id]'s row as it was in [snapshot], put back into [current] at its
         * old place — every other row as it is NOW, so a second answer that
         * landed meanwhile is not undone with it.
         */
        fun rollback(current: List<CreditRequestRow>, snapshot: List<CreditRequestRow>, id: String): List<CreditRequestRow> {
            val original = snapshot.firstOrNull { it.request.id == id } ?: return current
            val without = current.filterNot { it.request.id == id }
            // Its place: after the row that preceded it in the snapshot, when that row is still here.
            val before = snapshot.takeWhile { it.request.id != id }.map { it.request.id }
            val anchor = without.indexOfLast { it.request.id in before }
            return without.toMutableList().apply { add(anchor + 1, original) }
        }
    }
}
