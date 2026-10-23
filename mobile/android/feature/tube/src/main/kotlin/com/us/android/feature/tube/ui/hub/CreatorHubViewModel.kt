package com.us.android.feature.tube.ui.hub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.copyright.CopyrightReviews
import com.us.android.core.feed.data.copyright.over
import com.us.android.core.model.CopyrightReview
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.network.Paged
import com.us.android.feature.tube.data.hub.CreatorCommentTools
import com.us.android.feature.tube.data.hub.CreatorHubRepository
import com.us.android.feature.tube.data.hub.CreatorInsights
import com.us.android.feature.tube.data.hub.HubDraft
import com.us.android.feature.tube.data.hub.HubInboxRow
import com.us.android.feature.tube.data.hub.HubLibraryKind
import com.us.android.feature.tube.data.hub.HubLibraryRow
import com.us.android.feature.tube.data.hub.HubOpenKind
import com.us.android.feature.tube.data.hub.HubPeriod
import com.us.android.feature.tube.data.hub.hubOpenKind
import com.us.android.feature.tube.data.hub.withHeart
import com.us.android.feature.tube.data.hub.withPin
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
    /** Comments with a Heart or Pin on the wire; their buttons wait. */
    val commentBusy: Set<String> = emptySet(),
    /** Why the last Heart or Pin did not go through; the screen shows it, then [CreatorHubViewModel.dismissNotice]. */
    val notice: String? = null,
)

/**
 * The Creator Hub (T-31, 2026-10-15): the channel's numbers for a period,
 * the creator's videos, reels and drafts, and the newest comments on their
 * posts, which the creator can heart and pin from the row (2026-10-17).
 * Nothing else here edits, deletes or publishes — bulk edit and the
 * end-screen editor stay on the web, and payouts are off. Each list loads
 * when its tab is first shown and pages on "Load more".
 */
@HiltViewModel
class CreatorHubViewModel @Inject constructor(
    private val repository: CreatorHubRepository,
    private val reelsEntry: ReelsEntry,
    private val follows: FollowGraph,
    private val commentTools: CreatorCommentTools,
    private val urls: MediaUrlResolver,
    copyrightReviews: CopyrightReviews,
) : ViewModel() {

    private val _state = MutableStateFlow(CreatorHubUiState())
    val state: StateFlow<CreatorHubUiState> = _state.asStateFlow()

    /**
     * Copyright reviews learned this session (a "Request review" sent from a
     * row, the Edit sheet or the watch screen), layered over each row's own.
     */
    val reviews: StateFlow<Map<String, CopyrightReview>> = copyrightReviews.reviews

    /** A row's review as it stands now: this session's answer, else the row's. */
    fun reviewOf(row: HubLibraryRow, learned: Map<String, CopyrightReview>): CopyrightReview? =
        learned.over(row.id, row.copyrightReview)

    /**
     * Opened from a copyright notice (`/posttube/hub`, 2026-10-24): the
     * Library's Videos list, where the held or blocked video is.
     */
    fun openUploads() {
        _state.update { it.copy(libraryTab = HubLibraryTab.VIDEOS) }
        onSection(HubSection.LIBRARY)
    }

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

    /** A library row's still as a URL the image loader can fetch; null draws the plain tile. */
    fun thumbnailUrl(row: HubLibraryRow): String? = urls.hlsUrl(row.thumbnailPath)

    /** Heart or un-heart a comment: shown at once, put back if the server says no. */
    fun toggleHeart(commentId: String) {
        val row = _state.value.inbox.items.firstOrNull { it.commentId == commentId } ?: return
        val hearted = !row.hearted
        flipComment(commentId, { it.withHeart(commentId, hearted) }, HEART_FAILED) {
            commentTools.setHearted(commentId, hearted)
        }
    }

    /**
     * Pin or unpin a comment. A pin replaces the post's other pin on the
     * server, so the row of that post that had it loses it here too; a
     * failure puts every row back as it was.
     */
    fun togglePin(commentId: String) {
        val row = _state.value.inbox.items.firstOrNull { it.commentId == commentId } ?: return
        if (row.isReply) return
        val pinned = !row.pinned
        flipComment(commentId, { it.withPin(commentId, pinned) }, PIN_FAILED) {
            commentTools.setPinned(commentId, pinned)
        }
    }

    fun dismissNotice() = _state.update { it.copy(notice = null) }

    private fun flipComment(
        commentId: String,
        flip: (List<HubInboxRow>) -> List<HubInboxRow>,
        failedCopy: String,
        write: suspend () -> AppResult<Unit>,
    ) {
        if (commentId in _state.value.commentBusy) return
        val postId = _state.value.inbox.items.firstOrNull { it.commentId == commentId }?.postId ?: return
        // What the flip can touch — this post's rows — as they were, for the rollback.
        val before = _state.value.inbox.items.filter { it.postId == postId }.associateBy { it.commentId }
        _state.update {
            it.copy(inbox = it.inbox.copy(items = flip(it.inbox.items)), commentBusy = it.commentBusy + commentId)
        }
        viewModelScope.launch {
            val result = write()
            _state.update { state ->
                val items = if (result is AppResult.Failure) {
                    state.inbox.items.map { before[it.commentId] ?: it }
                } else {
                    state.inbox.items
                }
                state.copy(
                    inbox = state.inbox.copy(items = items),
                    commentBusy = state.commentBusy - commentId,
                    notice = if (result is AppResult.Failure) failedCopy else state.notice,
                )
            }
        }
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
        const val HEART_FAILED = "Couldn't update the heart. Try again."
        const val PIN_FAILED = "Couldn't update the pin. Try again."
    }
}
