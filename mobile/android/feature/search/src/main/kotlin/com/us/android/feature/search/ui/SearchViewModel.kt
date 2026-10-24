package com.us.android.feature.search.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.media.ReelsEntry
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.SessionState
import com.us.android.feature.search.data.RecentSearch
import com.us.android.feature.search.data.SavedSearch
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.data.SearchRepository
import com.us.android.feature.search.data.SearchSuggestion
import com.us.android.feature.search.navigation.SearchOrigin
import com.us.android.feature.search.navigation.SearchRoute
import com.us.android.feature.search.navigation.SearchScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The search page (T-16, 2026-10-15). The tabs come from where the page
 * was opened ([SearchOrigin.scopes]); the SUBMITTED words and the tab
 * together are one request, run through [SearchQueries]'s rules — on
 * submit, never per keystroke. While typing, autocomplete suggests. With
 * the box empty: the signed-in viewer's recent searches (search-service
 * keeps them; this page never writes one itself — a search made IS the
 * record) and the popular searches. Signed out there are no recents.
 *
 * A user row's Follow writes through the shared [FollowGraph], so the same
 * edge the feed shows is the one this page shows; a tapped reel is left in
 * [ReelsEntry] for the Reels tab, the handoff the Home feed makes.
 */
@HiltViewModel
class SearchViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: SearchRepository,
    private val follows: FollowGraph,
    private val reelsEntry: ReelsEntry,
    session: SessionStateProvider,
) : ViewModel() {

    private val route = savedStateHandle.toRoute<SearchRoute>()

    val origin: SearchOrigin = SearchOrigin.fromWire(route.origin)
    val scopes: List<SearchScope> = origin.scopes

    private val _query = MutableStateFlow(route.query)
    val query: StateFlow<String> = _query.asStateFlow()

    /** The words last searched for. Explore's field already submitted [SearchRoute.query]. */
    private val _submitted = MutableStateFlow(route.query.trim())

    private val _scope = MutableStateFlow(scopes.first())
    val scope: StateFlow<SearchScope> = _scope.asStateFlow()

    private val _attempt = MutableStateFlow(0)

    val body: StateFlow<SearchBody> = combine(_query, _submitted, SearchQueries::body)
        .stateIn(viewModelScope, SharingStarted.Eagerly, SearchQueries.body(route.query, route.query.trim()))

    val results: StateFlow<SearchResults> = SearchQueries.results(
        requests = combine(_submitted, _scope, _attempt) { text, scope, attempt ->
            SearchRequest(text, scope, attempt)
        },
        load = { request -> repository.search(request.scope, request.query) },
    )
        .map { state -> SearchQueries.withoutSelf(state, follows.ownId) }
        .onEach { state -> if (state is SearchResults.Loaded) onLoaded(state.hits) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), SearchResults.Idle)

    val suggestions: StateFlow<List<SearchSuggestion>> = SearchQueries.suggestions(
        texts = combine(_query, _submitted, SearchQueries::suggestionText),
        load = repository::suggestions,
    ).stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), emptyList())

    /** Signed in: only then are there recents to show, remove or clear. */
    val signedIn: StateFlow<Boolean> = session.sessionState
        .map { it is SessionState.Authenticated }
        .distinctUntilChanged()
        .stateIn(viewModelScope, SharingStarted.Eagerly, session.sessionState.value is SessionState.Authenticated)

    private val _recent = MutableStateFlow<List<RecentSearch>>(emptyList())
    val recent: StateFlow<List<RecentSearch>> = _recent.asStateFlow()

    private val _saved = MutableStateFlow<List<SavedSearch>>(emptyList())

    /** The viewer's saved searches (2026-10-24); empty signed out. */
    val saved: StateFlow<List<SavedSearch>> = _saved.asStateFlow()

    private val _popular = MutableStateFlow<List<String>>(emptyList())
    val popular: StateFlow<List<String>> = _popular.asStateFlow()

    /** "Clear all" asks first: it cannot be undone. */
    private val _confirmClear = MutableStateFlow(false)
    val confirmClear: StateFlow<Boolean> = _confirmClear.asStateFlow()

    /** A one-line note for a write that did not go through; the page shows it, then [onNoticeShown]. */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    /** The ids with a follow in flight — the button shows a spinner and takes no second tap. */
    private val _followBusy = MutableStateFlow<Set<String>>(emptySet())
    val followBusy: StateFlow<Set<String>> = _followBusy.asStateFlow()

    /** Recents writes in flight; a reload meanwhile would bring a removed entry back. */
    private var historyWrites = 0

    init {
        viewModelScope.launch { _popular.value = repository.popular() }
        viewModelScope.launch {
            signedIn.collect { yes ->
                if (yes) {
                    loadRecents()
                    loadSaved()
                } else {
                    _recent.value = emptyList()
                    _saved.value = emptyList()
                }
            }
        }
        // Back to the empty box after a search: the server has recorded it since.
        viewModelScope.launch {
            body.drop(1).filter { it == SearchBody.Start }.collect { if (signedIn.value) loadRecents() }
        }
    }

    fun onQueryChanged(text: String) {
        _query.value = text
        if (text.isBlank()) _submitted.value = ""
    }

    fun onScopeSelected(scope: SearchScope) {
        _scope.value = scope
    }

    fun onClear() {
        _query.value = ""
        _submitted.value = ""
    }

    /** Retry after a failure: the same request, asked again. */
    fun retry() {
        _attempt.update { it + 1 }
    }

    /** The keyboard's Search. */
    fun onSubmit() = submit(_query.value)

    fun onRecentPicked(recent: RecentSearch) = submit(recent.query)

    fun onPopularPicked(query: String) = submit(query)

    fun onSavedPicked(item: SavedSearch) = submit(item.query)

    /** Whether the search on screen is one of the saved ones (case-insensitive). */
    fun isSaved(query: String): Boolean = savedMatch(_saved.value, query) != null

    /**
     * Save the search on screen, or unsave it when it already is. Waits for
     * the server: a saved search that vanished on the next visit would be a lie.
     */
    fun toggleSaved() {
        val words = _submitted.value.trim()
        if (words.isEmpty() || !signedIn.value) return
        val existing = savedMatch(_saved.value, words)
        viewModelScope.launch {
            if (existing != null) {
                when (repository.deleteSaved(existing.id)) {
                    is AppResult.Success -> {
                        _saved.update { list -> list.filterNot { it.id == existing.id } }
                        _notice.value = SEARCH_UNSAVED
                    }
                    is AppResult.Failure -> _notice.value = SAVE_FAILED
                }
            } else {
                when (val result = repository.save(words)) {
                    is AppResult.Success -> {
                        _saved.update { list -> listOf(result.data) + list.filterNot { it.id == result.data.id } }
                        _notice.value = SEARCH_SAVED
                    }
                    is AppResult.Failure -> _notice.value = SAVE_FAILED
                }
            }
        }
    }

    /** Removes one saved search from the empty box's list. */
    fun removeSaved(item: SavedSearch) {
        val before = _saved.value
        _saved.update { list -> list.filterNot { it.id == item.id } }
        viewModelScope.launch {
            if (repository.deleteSaved(item.id) is AppResult.Failure) {
                _saved.value = before
                _notice.value = SAVE_FAILED
            }
        }
    }

    private suspend fun loadSaved() {
        when (val result = repository.saved()) {
            is AppResult.Success -> if (signedIn.value) _saved.value = result.data
            is AppResult.Failure -> Unit
        }
    }

    /** A suggestion that is a query (not a person or a tag, which open their own pages). */
    fun onSuggestionPicked(text: String) = submit(text)

    /** Removes ONE recent search; the rest stay. Shown at once, put back if the server refuses. */
    fun removeRecent(item: RecentSearch) {
        val before = _recent.value
        val index = before.indexOfFirst { it.id == item.id }
        if (index < 0) return
        _recent.value = SearchQueries.without(before, item.id)
        historyWrites++
        viewModelScope.launch {
            val result = repository.deleteHistoryItem(item.id)
            historyWrites--
            if (result is AppResult.Failure) {
                _recent.value = SearchQueries.restore(_recent.value, item, index)
                _notice.value = "Couldn't remove that search. Try again."
            }
        }
    }

    fun onClearAllRequested() {
        if (_recent.value.isNotEmpty()) _confirmClear.value = true
    }

    fun onClearAllDismissed() {
        _confirmClear.value = false
    }

    /** Confirmed "Clear all": every recent goes; all of them come back if the server refuses. */
    fun onClearAllConfirmed() {
        _confirmClear.value = false
        val before = _recent.value
        if (before.isEmpty()) return
        _recent.value = emptyList()
        historyWrites++
        viewModelScope.launch {
            val result = repository.clearHistory()
            historyWrites--
            if (result is AppResult.Failure) {
                _recent.value = before
                _notice.value = "Couldn't clear your searches. Try again."
            }
        }
    }

    fun onNoticeShown() {
        _notice.value = null
    }

    /** A reel row: leave the id for Reels, then the page asks `:app` for the tab. */
    fun openReel(postId: String) = reelsEntry.open(postId)

    fun follow(userId: String) = writeFollow(userId) { follows.follow(userId) }

    /** "Following" tapped (2026-10-17): the same shared graph, the other way. */
    fun unfollow(userId: String) = writeFollow(userId) { follows.unfollow(userId) }

    private fun writeFollow(userId: String, write: suspend () -> AppResult<Unit>) {
        if (userId in _followBusy.value) return
        _followBusy.update { it + userId }
        viewModelScope.launch {
            if (write() is AppResult.Failure) _notice.value = "Couldn't update that follow. Try again."
            _followBusy.update { it - userId }
        }
    }

    private fun submit(text: String) {
        val words = text.trim()
        if (words.isEmpty()) return
        _query.value = words
        // The same words again is a fresh search, not a no-op.
        if (_submitted.value == words) _attempt.update { it + 1 } else _submitted.value = words
    }

    private suspend fun loadRecents() {
        if (historyWrites > 0) return
        when (val result = repository.history()) {
            is AppResult.Success -> if (historyWrites == 0 && signedIn.value) _recent.value = result.data
            // Keep what is shown: a failed refresh is not "no recent searches".
            is AppResult.Failure -> Unit
        }
    }

    /** The user rows' edges, so Follow shows only where the answer is a known "not following". */
    private fun onLoaded(hits: List<SearchHit>) {
        val ids = hits.filterIsInstance<SearchHit.User>().map { it.id }
        if (ids.isNotEmpty()) viewModelScope.launch { follows.ensureKnown(ids) }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
        const val SEARCH_SAVED = "Search saved"
        const val SEARCH_UNSAVED = "Removed from saved searches"
        const val SAVE_FAILED = "Couldn't update your saved searches. Try again."
    }
}

/** The saved search with these words, if any — case and spaces aside. Pure, so it is a table test. */
internal fun savedMatch(saved: List<SavedSearch>, query: String): SavedSearch? {
    val words = query.trim()
    if (words.isEmpty()) return null
    return saved.firstOrNull { it.query.trim().equals(words, ignoreCase = true) }
}
