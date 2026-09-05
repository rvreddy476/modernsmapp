package com.us.android.feature.search.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.media.ReelsEntry
import com.us.android.core.model.FollowStatus
import com.us.android.feature.search.data.RecentSearchStore
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.data.SearchRepository
import com.us.android.feature.search.navigation.SearchOrigin
import com.us.android.feature.search.navigation.SearchRoute
import com.us.android.feature.search.navigation.SearchScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The search page (founder, 2026-09-05). The chips come from where the
 * page was opened ([SearchOrigin.scopes]); the text and the chip together
 * are one request, run through [SearchQueries]'s rules. A user row's
 * Follow writes through the shared [FollowGraph], so the same edge the
 * feed shows is the one this page shows; a tapped reel is left in
 * [ReelsEntry] for the Reels tab, the handoff the Home feed makes.
 */
@HiltViewModel
class SearchViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: SearchRepository,
    private val recents: RecentSearchStore,
    private val follows: FollowGraph,
    private val reelsEntry: ReelsEntry,
) : ViewModel() {

    private val route = savedStateHandle.toRoute<SearchRoute>()

    val origin: SearchOrigin = SearchOrigin.fromWire(route.origin)
    val scopes: List<SearchScope> = origin.scopes

    private val _query = MutableStateFlow(route.query)
    val query: StateFlow<String> = _query.asStateFlow()

    private val _scope = MutableStateFlow(scopes.first())
    val scope: StateFlow<SearchScope> = _scope.asStateFlow()

    private val _attempt = MutableStateFlow(0)

    val results: StateFlow<SearchResults> = SearchQueries.results(
        requests = combine(_query, _scope, _attempt) { text, scope, attempt ->
            SearchRequest(text.trim(), scope, attempt)
        },
        load = { request -> repository.search(request.scope, request.query) },
    )
        .onEach { state -> if (state is SearchResults.Loaded) onLoaded(state.hits) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), SearchResults.Idle)

    val recent: StateFlow<List<String>> = recents.recent
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), emptyList())

    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    /** The ids with a follow in flight — the button shows a spinner and takes no second tap. */
    private val _followBusy = MutableStateFlow<Set<String>>(emptySet())
    val followBusy: StateFlow<Set<String>> = _followBusy.asStateFlow()

    init {
        // Explore's field submitted a query: it counts as a search made.
        if (route.query.isNotBlank()) remember(route.query)
    }

    fun onQueryChanged(text: String) {
        _query.value = text
    }

    fun onScopeSelected(scope: SearchScope) {
        _scope.value = scope
    }

    fun onClear() {
        _query.value = ""
    }

    /** Retry after a failure: the same request, asked again. */
    fun retry() {
        _attempt.update { it + 1 }
    }

    /** The keyboard's Search: the query joins the recents. */
    fun onSubmit() = remember(_query.value)

    fun onRecentPicked(query: String) {
        _query.value = query
        remember(query)
    }

    fun onClearRecents() {
        viewModelScope.launch { recents.clear() }
    }

    /** A result was tapped: the query that found it is worth keeping. */
    fun onHitOpened() = remember(_query.value)

    /** A reel row: leave the id for Reels, then the page asks `:app` for the tab. */
    fun openReel(postId: String) = reelsEntry.open(postId)

    fun follow(userId: String) {
        if (userId in _followBusy.value) return
        _followBusy.update { it + userId }
        viewModelScope.launch {
            follows.follow(userId)
            _followBusy.update { it - userId }
        }
    }

    private fun remember(query: String) {
        val trimmed = query.trim()
        if (trimmed.length < SearchQueries.MIN_CHARS) return
        viewModelScope.launch { recents.record(trimmed) }
    }

    /** The user rows' edges, so Follow shows only where the answer is a known "not following". */
    private fun onLoaded(hits: List<SearchHit>) {
        val ids = hits.filterIsInstance<SearchHit.User>().map { it.id }
        if (ids.isNotEmpty()) viewModelScope.launch { follows.ensureKnown(ids) }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}
