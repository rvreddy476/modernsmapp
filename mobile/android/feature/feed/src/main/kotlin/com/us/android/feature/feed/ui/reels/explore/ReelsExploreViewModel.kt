package com.us.android.feature.feed.ui.reels.explore

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.DiscoveryRepository
import com.us.android.core.feed.data.EXPLORE_ALL
import com.us.android.core.feed.data.ExplorePage
import com.us.android.core.feed.data.Topic
import com.us.android.core.feed.data.TopicKind
import com.us.android.core.feed.data.exploreTiles
import com.us.android.core.feed.data.isInvalidCategory
import com.us.android.core.feed.data.normalizeExploreCategory
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsFilterChip
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the grid's first page came to. */
sealed interface ExplorePhase {
    data object Loading : ExplorePhase
    data object Ready : ExplorePhase

    /** The category is malformed: the server refused it, or it was never sent. */
    data object Invalid : ExplorePhase
    data class Failed(val message: String) : ExplorePhase
}

/** Which body the page draws — the web's ExploreReels branches, in its order. */
enum class ExploreBody { LOADING, INVALID, FAILED, KEEP_LOOKING, EMPTY, GRID }

/**
 * The Explore page (TikTok parity T-18 on the web, 2026-10-24 on Android),
 * as one value.
 *
 * @property category the selected chip's slug; [EXPLORE_ALL] is All.
 * @property pages every viewer action's page so far, in order.
 */
data class ReelsExploreState(
    val categories: List<Topic> = emptyList(),
    val category: String = EXPLORE_ALL,
    val phase: ExplorePhase = ExplorePhase.Loading,
    val pages: List<ExplorePage> = emptyList(),
    val loadingMore: Boolean = false,
    val moreFailed: Boolean = false,
    val refreshing: Boolean = false,
) {
    val tiles: List<FeedItem> get() = exploreTiles(pages)

    /** The last page named a next one. */
    val canLoadMore: Boolean get() = pages.lastOrNull()?.nextCursor != null

    /** The last page brought nothing (but may have a cursor): the grid stops loading by itself. */
    val lastPageEmpty: Boolean get() = pages.lastOrNull()?.items?.isEmpty() == true

    /**
     * The grid reaching its end should ask for more: there is more, nothing
     * is on its way or just failed, and the last page brought reels — an
     * empty one waits for the viewer, so an empty category never spins
     * through the timeline on its own.
     */
    val wantsMore: Boolean get() = canLoadMore && !lastPageEmpty && !loadingMore && !moreFailed

    /** The selected chip's label; the slug itself while the chips are not in. */
    val categoryLabel: String get() = categories.firstOrNull { it.slug == category }?.label ?: category

    /** "All" first, then the short-form categories in the server's order. */
    val chips: List<UsFilterChip>
        get() = listOf(UsFilterChip(EXPLORE_ALL, ALL_LABEL)) + categories.map { UsFilterChip(it.slug, it.label) }
}

/** Which body [state] draws. Pure: the whole branching, without a screen. */
fun exploreBody(state: ReelsExploreState): ExploreBody = when {
    state.phase == ExplorePhase.Invalid -> ExploreBody.INVALID
    state.phase == ExplorePhase.Loading -> ExploreBody.LOADING
    state.phase is ExplorePhase.Failed -> ExploreBody.FAILED
    state.tiles.isEmpty() && state.canLoadMore -> ExploreBody.KEEP_LOOKING
    state.tiles.isEmpty() -> ExploreBody.EMPTY
    else -> ExploreBody.GRID
}

/** The first page's failure, as the page says it. */
fun explorePhase(error: AppError): ExplorePhase = when {
    error.isInvalidCategory() -> ExplorePhase.Invalid
    error is AppError.NoNetwork -> ExplorePhase.Failed("You're offline. Check your connection and try again.")
    error is AppError.Timeout -> ExplorePhase.Failed("That took too long. Try again.")
    else -> ExplorePhase.Failed("Couldn't load reels.")
}

/**
 * Reels Explore: category chips over a grid of short videos —
 * `GET /v1/posts/categories?kind=short` for the chips and
 * `GET /v1/feed/reels?category=` for the grid ([DiscoveryRepository]).
 *
 * One request walk per viewer action: a chip tap loads the first page
 * (walking past empty pages up to the cap); after that the grid asks for
 * more as its end comes into view, but only while pages bring reels. A tile
 * leaves its reel's id in [ReelsEntry]; the screen switches to the Reels tab.
 */
@HiltViewModel
class ReelsExploreViewModel @Inject constructor(
    private val discovery: DiscoveryRepository,
    private val urlResolver: MediaUrlResolver,
    private val reelsEntry: ReelsEntry,
) : ViewModel() {

    private val _state = MutableStateFlow(ReelsExploreState())
    val state: StateFlow<ReelsExploreState> = _state.asStateFlow()

    private var loading: Job? = null

    init {
        viewModelScope.launch {
            (discovery.topics(TopicKind.SHORT) as? AppResult.Success)?.let { result ->
                _state.update { it.copy(categories = result.data) }
            }
        }
        load()
    }

    /** A chip. The chip already selected is left alone. */
    fun selectCategory(slug: String) {
        if (slug == _state.value.category) return
        _state.update { it.copy(category = slug) }
        load()
    }

    /** "Show all" from the invalid and empty states. */
    fun showAll() = selectCategory(EXPLORE_ALL)

    /** The first page of the selected chip, from the top. Also Retry. */
    fun load() {
        loading?.cancel()
        val category = _state.value.category
        _state.update { it.copy(phase = ExplorePhase.Loading, pages = emptyList(), loadingMore = false, moreFailed = false) }
        if (!normalizeExploreCategory(category).valid) {
            _state.update { it.copy(phase = ExplorePhase.Invalid) }
            return
        }
        loading = viewModelScope.launch {
            when (val result = discovery.explorePage(category, cursor = null)) {
                is AppResult.Success -> _state.update { it.copy(phase = ExplorePhase.Ready, pages = listOf(result.data)) }
                is AppResult.Failure -> _state.update { it.copy(phase = explorePhase(result.error)) }
            }
        }
    }

    /** Pull to refresh: the first page again, the tiles staying until it lands; a failure keeps them. */
    fun refresh() {
        if (_state.value.phase != ExplorePhase.Ready) return load()
        if (loading?.isActive == true) return
        val category = _state.value.category
        _state.update { it.copy(refreshing = true) }
        loading = viewModelScope.launch {
            when (val result = discovery.explorePage(category, cursor = null)) {
                is AppResult.Success -> _state.update {
                    it.copy(pages = listOf(result.data), refreshing = false, moreFailed = false)
                }
                is AppResult.Failure -> _state.update { it.copy(refreshing = false) }
            }
        }
    }

    /**
     * The next page: when the grid reaches its end ([ReelsExploreState.wantsMore]),
     * and when the viewer taps "Keep looking" or "Show more" after an empty one.
     */
    fun loadMore() {
        val cursor = _state.value.pages.lastOrNull()?.nextCursor ?: return
        if (loading?.isActive == true) return
        val category = _state.value.category
        _state.update { it.copy(loadingMore = true, moreFailed = false) }
        loading = viewModelScope.launch {
            when (val result = discovery.explorePage(category, cursor)) {
                is AppResult.Success -> _state.update { it.copy(pages = it.pages + result.data, loadingMore = false) }
                // The tiles already shown stay: a later page failing is not the page failing.
                is AppResult.Failure -> _state.update { it.copy(loadingMore = false, moreFailed = true) }
            }
        }
    }

    /** A tile: Reels will open on that reel. */
    fun openReel(postId: String) = reelsEntry.open(postId)

    /** The still a tile draws: the author's cover, else the transcode's own. */
    fun posterUrl(item: FeedItem): String? = urlResolver.videoThumb(item).url
}

internal const val ALL_LABEL = "All"
