package com.us.android.feature.tube.ui.hub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.media.ReelsEntry
import com.us.android.core.network.Paged
import com.us.android.feature.tube.data.hub.CreatorHubRepository
import com.us.android.feature.tube.data.hub.CreatorInsights
import com.us.android.feature.tube.data.hub.HubDraft
import com.us.android.feature.tube.data.hub.HubInboxRow
import com.us.android.feature.tube.data.hub.HubLibraryKind
import com.us.android.feature.tube.data.hub.HubLibraryRow
import com.us.android.feature.tube.data.hub.HubOpenKind
import com.us.android.feature.tube.data.hub.HubPeriod
import com.us.android.feature.tube.data.hub.hubOpenKind
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The hub's three pages, in the web's words. */
enum class HubSection(val label: String) {
    OVERVIEW("Overview"),
    LIBRARY("Library"),
    CONVERSATIONS("Conversations"),

    /** The curated sound library (2026-10-15): browse, preview, and "Use this sound" into a new reel. */
    SOUNDS("Sounds"),
}

/** The library's lists. "Reels" is this app's word for the web's "Shorts" tab (the same `flicks` route). */
enum class HubLibraryTab(val label: String) {
    VIDEOS("Videos"),
    REELS("Reels"),
    DRAFTS("Drafts"),
}

/** The overview's one answer. */
sealed interface OverviewState {
    data object Loading : OverviewState
    data class Failed(val message: String) : OverviewState
    data class Ready(val insights: CreatorInsights) : OverviewState
}

/**
 * One cursor-paged list. [loaded] is false until the first page has been
 * asked for, so a tab nobody opened costs nothing; [moreFailed] keeps the
 * rows already shown when only the NEXT page failed.
 */
data class HubPage<T>(
    val loaded: Boolean = false,
    val loading: Boolean = false,
    val items: List<T> = emptyList(),
    val nextCursor: String? = null,
    val error: String? = null,
    val loadingMore: Boolean = false,
    val moreFailed: Boolean = false,
) {
    val hasMore: Boolean get() = nextCursor != null
}

data class CreatorHubUiState(
    val section: HubSection = HubSection.OVERVIEW,
    val period: HubPeriod = HubPeriod.DEFAULT,
    val overview: OverviewState = OverviewState.Loading,
    val libraryTab: HubLibraryTab = HubLibraryTab.VIDEOS,
    val videos: HubPage<HubLibraryRow> = HubPage(),
    val reels: HubPage<HubLibraryRow> = HubPage(),
    val drafts: HubPage<HubDraft> = HubPage(),
    val inbox: HubPage<HubInboxRow> = HubPage(),
)

/**
 * The read-only Creator Hub (T-31, 2026-10-15): the channel's numbers for a
 * period, the creator's videos, reels and drafts, and the newest comments on
 * their posts. Nothing here edits, deletes or publishes — bulk edit and the
 * end-screen editor stay on the web, and payouts are off. Each list loads
 * when its tab is first shown and pages on "Load more".
 */
@HiltViewModel
class CreatorHubViewModel @Inject constructor(
    private val repository: CreatorHubRepository,
    private val reelsEntry: ReelsEntry,
    private val follows: FollowGraph,
) : ViewModel() {

    private val _state = MutableStateFlow(CreatorHubUiState())
    val state: StateFlow<CreatorHubUiState> = _state.asStateFlow()

    /** The creator: the comments sheet gives the post's author Pin and Heart. */
    val ownUserId: String get() = follows.ownId

    private var overviewJob: Job? = null

    init {
        loadOverview()
    }

    fun onSection(section: HubSection) {
        _state.update { it.copy(section = section) }
        when (section) {
            HubSection.OVERVIEW -> Unit
            HubSection.LIBRARY -> ensureLoaded(_state.value.libraryTab)
            HubSection.CONVERSATIONS -> if (!_state.value.inbox.loaded) loadInbox(more = false)
            // The Sounds list is its own ViewModel, made when the tab is first shown.
            HubSection.SOUNDS -> Unit
        }
    }

    fun onPeriod(period: HubPeriod) {
        if (period == _state.value.period && _state.value.overview !is OverviewState.Failed) return
        _state.update { it.copy(period = period) }
        loadOverview()
    }

    fun retryOverview() = loadOverview()

    fun onLibraryTab(tab: HubLibraryTab) {
        _state.update { it.copy(libraryTab = tab) }
        ensureLoaded(tab)
    }

    /** First page again (Try again), or the next one (Load more). */
    fun loadLibrary(tab: HubLibraryTab, more: Boolean) = when (tab) {
        HubLibraryTab.VIDEOS -> loadUploads(HubLibraryKind.VIDEOS, more)
        HubLibraryTab.REELS -> loadUploads(HubLibraryKind.FLICKS, more)
        HubLibraryTab.DRAFTS -> loadPage(more, { it.drafts }, { s, p -> s.copy(drafts = p) }, DRAFTS_FAILED) {
            repository.drafts(it)
        }
    }

    fun loadInbox(more: Boolean) =
        loadPage(more, { it.inbox }, { s, p -> s.copy(inbox = p) }, INBOX_FAILED) { repository.inbox(it) }

    /**
     * Where a row opens. A reel is parked in [ReelsEntry] first — the Reels
     * tab carries no argument, the way every feed hands a reel over — and
     * the screen then asks `:app` for the surface the returned kind names.
     */
    fun open(postId: String, contentType: String, fallback: HubOpenKind): HubOpenKind {
        val kind = hubOpenKind(contentType, fallback)
        if (kind == HubOpenKind.REEL) reelsEntry.open(postId)
        return kind
    }

    private fun ensureLoaded(tab: HubLibraryTab) {
        val page = when (tab) {
            HubLibraryTab.VIDEOS -> _state.value.videos
            HubLibraryTab.REELS -> _state.value.reels
            HubLibraryTab.DRAFTS -> _state.value.drafts
        }
        if (!page.loaded) loadLibrary(tab, more = false)
    }

    private fun loadOverview() {
        val period = _state.value.period
        overviewJob?.cancel()
        _state.update { it.copy(overview = OverviewState.Loading) }
        overviewJob = viewModelScope.launch {
            val next = when (val result = repository.insights(period)) {
                is AppResult.Success -> OverviewState.Ready(result.data)
                is AppResult.Failure -> OverviewState.Failed(OVERVIEW_FAILED)
            }
            // A period changed while this was in flight cancels it; this guard is belt and braces.
            if (_state.value.period == period) _state.update { it.copy(overview = next) }
        }
    }

    private fun loadUploads(kind: HubLibraryKind, more: Boolean) = when (kind) {
        HubLibraryKind.VIDEOS -> loadPage(more, { it.videos }, { s, p -> s.copy(videos = p) }, VIDEOS_FAILED) {
            repository.library(kind, it)
        }
        HubLibraryKind.FLICKS -> loadPage(more, { it.reels }, { s, p -> s.copy(reels = p) }, REELS_FAILED) {
            repository.library(kind, it)
        }
    }

    private fun <T> loadPage(
        more: Boolean,
        read: (CreatorHubUiState) -> HubPage<T>,
        write: (CreatorHubUiState, HubPage<T>) -> CreatorHubUiState,
        failedCopy: String,
        fetch: suspend (cursor: String?) -> AppResult<Paged<T>>,
    ) {
        val current = read(_state.value)
        if (current.loading || current.loadingMore) return
        if (more && current.nextCursor == null) return
        val cursor = if (more) current.nextCursor else null
        _state.update {
            val page = read(it)
            val started = if (more) {
                page.copy(loadingMore = true, moreFailed = false)
            } else {
                page.copy(loaded = true, loading = true, error = null)
            }
            write(it, started)
        }
        viewModelScope.launch {
            val result = fetch(cursor)
            _state.update {
                val page = read(it)
                write(it, page.settled(result, more, failedCopy))
            }
        }
    }

    private fun <T> HubPage<T>.settled(result: AppResult<Paged<T>>, more: Boolean, failedCopy: String): HubPage<T> =
        when (result) {
            is AppResult.Success -> copy(
                loading = false,
                loadingMore = false,
                error = null,
                moreFailed = false,
                items = if (more) items + result.data.items else result.data.items,
                nextCursor = result.data.nextCursor,
            )
            is AppResult.Failure ->
                if (more) copy(loadingMore = false, moreFailed = true) else copy(loading = false, error = failedCopy)
        }

    internal companion object {
        // The web hub's own sentences, where it has one.
        const val OVERVIEW_FAILED = "Channel performance couldn't load."
        const val VIDEOS_FAILED = "Your videos couldn't load."
        const val REELS_FAILED = "Your reels couldn't load."
        const val DRAFTS_FAILED = "Your drafts couldn't load."
        const val INBOX_FAILED = "Latest comments couldn't load."
    }
}
