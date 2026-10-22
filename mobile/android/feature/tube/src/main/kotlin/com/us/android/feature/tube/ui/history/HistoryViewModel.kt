package com.us.android.feature.tube.ui.history

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.WatchHistoryPauseStore
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.WatchHistoryEntry
import com.us.android.core.feed.data.WatchHistorySource
import com.us.android.core.feed.data.filterHistory
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.feature.tube.data.TubeQueue
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything the History page draws. */
data class HistoryUiState(
    /** The first read is in flight and nothing is on screen yet: the skeleton. */
    val loading: Boolean = true,
    /** The first read failed with nothing to show: the error with Retry. */
    val error: String? = null,
    val rows: List<WatchHistoryEntry> = emptyList(),
    val nextCursor: String? = null,
    val appending: Boolean = false,
    /** The next page failed; the footer offers Try again. */
    val appendFailed: Boolean = false,
    /** A pull to refresh over rows already on screen. */
    val refreshing: Boolean = false,
    /** History's search, over the rows already loaded (the web's rule). */
    val query: String = "",
    /** "Pause recording": while on, nothing watched is added. */
    val paused: Boolean = false,
    /** "Clear all" asked, waiting for the viewer's yes. */
    val confirmingClear: Boolean = false,
) {
    /** The rows the search keeps. */
    val shown: List<WatchHistoryEntry> get() = filterHistory(rows, query)

    val canLoadMore: Boolean get() = nextCursor != null && !appending && !appendFailed

    /** Nothing watched at all (not "nothing matches"). */
    val isEmpty: Boolean get() = !loading && error == null && rows.isEmpty()
}

/**
 * History (web parity, 2026-10-22, `RecentScreen.tsx`): every video the
 * viewer watched, most recent first, with where they left off.
 *
 * Paged by the server's cursor and kept as one list here (not a Paging
 * flow) because every write is optimistic: Remove takes a row out at once
 * and puts it back where it was if the server refuses; Clear all empties
 * the page at once and restores it on a refusal. Search filters what is
 * loaded, as the web's does. "Pause recording" is the device preference the
 * progress writer reads.
 */
@HiltViewModel
class HistoryViewModel @Inject constructor(
    private val source: WatchHistorySource,
    private val pause: WatchHistoryPauseStore,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
) : ViewModel() {

    private val _state = MutableStateFlow(HistoryUiState())
    val state: StateFlow<HistoryUiState> = _state.asStateFlow()

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    /** Rows whose removal is on the wire; a second tap waits for the first. */
    private val removing = mutableSetOf<String>()

    init {
        viewModelScope.launch { pause.paused.collect { paused -> _state.update { it.copy(paused = paused) } } }
        refresh()
    }

    /** Reads the first page again. Rows on screen stay while it does; only a first read shows the skeleton. */
    fun refresh() {
        viewModelScope.launch {
            val hadRows = _state.value.rows.isNotEmpty()
            _state.update { it.copy(refreshing = hadRows, loading = !hadRows, error = null) }
            when (val result = source.page(cursor = null)) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        loading = false,
                        refreshing = false,
                        error = null,
                        rows = result.data.items,
                        nextCursor = result.data.nextCursor,
                        appendFailed = false,
                    )
                }
                is AppResult.Failure -> if (_state.value.rows.isNotEmpty()) {
                    // A failed refresh keeps what was already read, and says so once.
                    _state.update { it.copy(loading = false, refreshing = false) }
                    _message.value = UsMessage(messageFor(result.error, "We couldn't refresh History."))
                } else {
                    _state.update {
                        it.copy(loading = false, refreshing = false, error = messageFor(result.error, LOAD_FAILED))
                    }
                }
            }
        }
    }

    /** The error state's Retry: the skeleton, then the read. */
    fun retry() {
        _state.update { it.copy(loading = true, error = null) }
        refresh()
    }

    /** The next page; the list's end and the footer's Try again both call it. */
    fun loadMore() {
        val current = _state.value
        val cursor = current.nextCursor ?: return
        if (current.appending) return
        _state.update { it.copy(appending = true, appendFailed = false) }
        viewModelScope.launch {
            when (val result = source.page(cursor)) {
                is AppResult.Success -> _state.update { now ->
                    val known = now.rows.mapTo(mutableSetOf()) { it.postId }
                    now.copy(
                        appending = false,
                        rows = now.rows + result.data.items.filterNot { it.postId in known },
                        nextCursor = result.data.nextCursor,
                    )
                }
                is AppResult.Failure -> _state.update { it.copy(appending = false, appendFailed = true) }
            }
        }
    }

    fun setQuery(query: String) = _state.update { it.copy(query = query) }

    /** "Pause recording": kept on this device; the watch screen stops writing progress while it is on. */
    fun setPaused(paused: Boolean) {
        _state.update { it.copy(paused = paused) }
        viewModelScope.launch { pause.setPaused(paused) }
    }

    /** One video out of History. The row goes at once and comes back where it was if the server refuses. */
    fun remove(entry: WatchHistoryEntry) {
        if (!removing.add(entry.postId)) return
        val index = _state.value.rows.indexOfFirst { it.postId == entry.postId }
        if (index < 0) {
            removing.remove(entry.postId)
            return
        }
        _state.update { now -> now.copy(rows = now.rows.filterNot { it.postId == entry.postId }) }
        viewModelScope.launch {
            val result = source.remove(entry.postId)
            removing.remove(entry.postId)
            if (result is AppResult.Failure && result.error !is AppError.NotFound) {
                _state.update { now ->
                    if (now.rows.any { it.postId == entry.postId }) {
                        now
                    } else {
                        now.copy(rows = now.rows.toMutableList().apply { add(index.coerceIn(0, size), entry) })
                    }
                }
                _message.value = UsMessage(messageFor(result.error, "Couldn't remove that from History. Try again."))
            }
        }
    }

    fun askClear() = _state.update { it.copy(confirmingClear = true) }

    fun cancelClear() = _state.update { it.copy(confirmingClear = false) }

    /** Every video out of History. The page empties at once; a refusal puts it all back and says so. */
    fun clearAll() {
        val before = _state.value
        _state.update { it.copy(confirmingClear = false, rows = emptyList(), nextCursor = null, query = "") }
        viewModelScope.launch {
            when (val result = source.clear()) {
                is AppResult.Success -> _message.value = UsMessage("History cleared", UsMessageType.Success)
                is AppResult.Failure -> {
                    _state.update {
                        it.copy(rows = before.rows, nextCursor = before.nextCursor, query = before.query)
                    }
                    _message.value = UsMessage(messageFor(result.error, "Couldn't clear History. Try again."))
                }
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun thumb(entry: WatchHistoryEntry): VideoThumb = urlResolver.videoThumb(entry.item)

    /** A row was tapped: the rows shown become the watch screen's queue, that one first. */
    fun onOpen(entry: WatchHistoryEntry) {
        val shown = _state.value.shown.map { it.item }
        queue.set(shown.sortedBy { it.id != entry.postId })
    }

    private fun messageFor(error: AppError, fallback: String): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        else -> fallback
    }

    private companion object {
        const val LOAD_FAILED = "We couldn't load History."
    }
}
