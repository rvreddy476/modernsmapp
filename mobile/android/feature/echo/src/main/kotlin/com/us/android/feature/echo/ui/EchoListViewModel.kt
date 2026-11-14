package com.us.android.feature.echo.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.NotePage
import com.us.android.feature.echo.data.NoteSearchTab
import com.us.android.feature.echo.data.Trend
import com.us.android.feature.echo.data.echoListErrorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The pushed list pages of Echo, one route each ([EchoListKind.wire] rides in the route). */
enum class EchoListKind(val wire: String, val tabbed: Boolean) {
    HASHTAG("hashtag", tabbed = true),
    SEARCH("search", tabbed = true),
    BOOKMARKS("bookmarks", tabbed = false),
    QUOTES("quotes", tabbed = false),
    ;

    companion object {
        fun fromWire(wire: String?): EchoListKind = entries.firstOrNull { it.wire == wire } ?: BOOKMARKS
    }
}

/** The page title: "#tag", the query, "Bookmarks", "Quotes". */
fun listTitle(kind: EchoListKind, arg: String): String = when (kind) {
    EchoListKind.HASHTAG -> "#" + arg.removePrefix("#")
    EchoListKind.SEARCH -> arg
    EchoListKind.BOOKMARKS -> "Bookmarks"
    EchoListKind.QUOTES -> "Quotes"
}

/**
 * A hashtag's page and search results (Top / Latest), Bookmarks (newest
 * bookmark first) and a note's Quotes. A note un-bookmarked on the
 * Bookmarks page leaves it.
 */
@HiltViewModel
class EchoListViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: EchoRepository,
    private val media: EchoMediaUrls,
    deps: EchoActionDeps,
) : ViewModel() {

    val kind: EchoListKind = EchoListKind.fromWire(savedStateHandle.get<String>(KIND_KEY))
    val arg: String = savedStateHandle.get<String>(ARG_KEY).orEmpty()

    private val _tab = MutableStateFlow(NoteSearchTab.TOP)
    val tab: StateFlow<NoteSearchTab> = _tab.asStateFlow()

    private val feeds: Map<NoteSearchTab, NoteFeedController> = NoteSearchTab.entries.associateWith { tab ->
        NoteFeedController(
            scope = viewModelScope,
            fetch = { cursor -> fetch(tab, cursor) },
            onPage = { notes -> viewModelScope.launch { media.load(notes) } },
            errorText = ::echoListErrorMessage,
        )
    }

    val mediaUrls: StateFlow<Map<String, String>> = media.urls
    val gate = ComposeGateController(viewModelScope, repository)
    val actions = EchoNoteActions(
        scope = viewModelScope,
        deps = deps,
        apply = { id, transform ->
            feeds.values.forEach { it.mapNote(id, transform) }
            // A bookmark taken off on the Bookmarks page takes the note off it.
            if (kind == EchoListKind.BOOKMARKS) feeds.values.forEach { feed -> feed.removeWhere { !it.bookmarked } }
        },
        remove = { predicate -> feeds.values.forEach { it.removeWhere(predicate) } },
    )

    init {
        feeds.getValue(_tab.value).loadIfNeeded()
    }

    fun feed(tab: NoteSearchTab): StateFlow<NoteFeed> = feeds.getValue(tab).feed

    fun select(tab: NoteSearchTab) {
        if (!kind.tabbed) return
        _tab.value = tab
        feeds.getValue(tab).loadIfNeeded()
    }

    fun refresh() = feeds.getValue(_tab.value).refresh()
    fun retry() = feeds.getValue(_tab.value).retry()
    fun loadMore() = feeds.getValue(_tab.value).loadMore()

    private suspend fun fetch(tab: NoteSearchTab, cursor: String?): AppResult<NotePage> = when (kind) {
        EchoListKind.HASHTAG -> repository.hashtag(arg, tab, cursor)
        EchoListKind.SEARCH -> repository.search(arg, tab, cursor)
        EchoListKind.BOOKMARKS -> repository.bookmarks(cursor)
        EchoListKind.QUOTES -> repository.quotes(arg, cursor)
    }

    companion object {
        const val KIND_KEY = "kind"
        const val ARG_KEY = "arg"
    }
}

/** Explore: the search box, Trending (24 hours of hashtags), and Latest posts. */
@HiltViewModel
class ExploreViewModel @Inject constructor(
    private val repository: EchoRepository,
    private val media: EchoMediaUrls,
    deps: EchoActionDeps,
) : ViewModel() {

    data class TrendsState(val items: List<Trend> = emptyList(), val loading: Boolean = true, val error: String? = null)

    private val _trends = MutableStateFlow(TrendsState())
    val trends: StateFlow<TrendsState> = _trends.asStateFlow()

    private val latestFeed = NoteFeedController(
        scope = viewModelScope,
        fetch = { cursor -> repository.search("", NoteSearchTab.LATEST, cursor) },
        onPage = { notes -> viewModelScope.launch { media.load(notes) } },
        errorText = { echoListErrorMessage(it) },
    )
    val latest: StateFlow<NoteFeed> = latestFeed.feed
    val mediaUrls: StateFlow<Map<String, String>> = media.urls
    val gate = ComposeGateController(viewModelScope, repository)
    val actions = EchoNoteActions(
        scope = viewModelScope,
        deps = deps,
        apply = { id, transform -> latestFeed.mapNote(id, transform) },
        remove = latestFeed::removeWhere,
    )

    init {
        loadTrends()
    }

    fun loadTrends() {
        _trends.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            _trends.value = when (val result = repository.trends()) {
                is AppResult.Success -> TrendsState(items = result.data, loading = false)
                is AppResult.Failure -> TrendsState(loading = false, error = echoListErrorMessage(result.error))
            }
        }
    }

    fun showLatest() = latestFeed.loadIfNeeded()
    fun refreshLatest() = latestFeed.refresh()
    fun retryLatest() = latestFeed.retry()
    fun loadMoreLatest() = latestFeed.loadMore()
}
