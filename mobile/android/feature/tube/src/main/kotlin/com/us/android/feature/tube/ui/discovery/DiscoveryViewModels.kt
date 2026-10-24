package com.us.android.feature.tube.ui.discovery

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.feed.data.DiscoveryPage
import com.us.android.core.feed.data.DiscoveryRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.Topic
import com.us.android.core.feed.data.TopicKind
import com.us.android.core.feed.data.TopicSort
import com.us.android.core.feed.data.TrendingPeriod
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.filterByPeriod
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.navigation.TubeTopicRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where a discovery list's first page stands. */
sealed interface DiscoveryPhase {
    data object Loading : DiscoveryPhase
    data object Ready : DiscoveryPhase
    data class Failed(val message: String) : DiscoveryPhase
}

/** A list of videos read a page at a time: what is loaded and what is on its way. */
data class DiscoveryListState(
    val phase: DiscoveryPhase = DiscoveryPhase.Loading,
    val items: List<FeedItem> = emptyList(),
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    val moreFailed: Boolean = false,
    val refreshing: Boolean = false,
) {
    val canLoadMore: Boolean get() = nextCursor != null

    /** The list's end in view should ask for more: there is more and nothing is on its way or just failed. */
    val wantsMore: Boolean get() = canLoadMore && !loadingMore && !moreFailed
}

/** A first page's failure, as the page says it. */
fun discoveryFailure(error: AppError, what: String): DiscoveryPhase.Failed = DiscoveryPhase.Failed(
    when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        else -> "We couldn't load $what."
    },
)

/**
 * One list's paging for the discovery pages: the first page, more as the
 * end comes into view, pull to refresh that keeps the rows until it lands.
 * Rows are each kept once, in order, across pages. Pure over [load], so the
 * whole of it is testable without a server.
 */
class DiscoveryPager(
    private val scope: CoroutineScope,
    private val what: String,
    private val load: suspend (cursor: String?) -> AppResult<DiscoveryPage>,
) {
    private val _state = MutableStateFlow(DiscoveryListState())
    val state: StateFlow<DiscoveryListState> = _state.asStateFlow()
    private var job: Job? = null

    fun start() {
        job?.cancel()
        _state.value = DiscoveryListState()
        job = scope.launch {
            _state.value = when (val result = load(null)) {
                is AppResult.Success -> DiscoveryListState(DiscoveryPhase.Ready, result.data.items.distinctBy { it.id }, result.data.nextCursor)
                is AppResult.Failure -> DiscoveryListState(discoveryFailure(result.error, what))
            }
        }
    }

    fun refresh() {
        if (_state.value.phase != DiscoveryPhase.Ready) return start()
        if (job?.isActive == true) return
        _state.update { it.copy(refreshing = true) }
        job = scope.launch {
            when (val result = load(null)) {
                is AppResult.Success ->
                    _state.value = DiscoveryListState(DiscoveryPhase.Ready, result.data.items.distinctBy { it.id }, result.data.nextCursor)
                is AppResult.Failure -> _state.update { it.copy(refreshing = false) }
            }
        }
    }

    fun loadMore() {
        val cursor = _state.value.nextCursor ?: return
        if (job?.isActive == true) return
        _state.update { it.copy(loadingMore = true, moreFailed = false) }
        job = scope.launch {
            when (val result = load(cursor)) {
                is AppResult.Success -> _state.update { now ->
                    val seen = now.items.map { it.id }.toSet()
                    now.copy(
                        items = now.items + result.data.items.filter { it.id !in seen }.distinctBy { it.id },
                        nextCursor = result.data.nextCursor,
                        loadingMore = false,
                    )
                }
                is AppResult.Failure -> _state.update { it.copy(loadingMore = false, moreFailed = true) }
            }
        }
    }
}

/** What every discovery page shares: thumbnails, the More sheet's graph, the watch screen's queue. */
abstract class DiscoveryVideosViewModel(
    private val urlResolver: MediaUrlResolver,
    private val follows: FollowGraph,
    private val queue: TubeQueue,
    engagement: EngagementStore,
) : ViewModel() {
    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A card: the list on screen goes with it as the watch screen's "Up next". */
    fun onOpen(shown: List<FeedItem>) = queue.set(shown)
}

/**
 * Trending (web parity, 2026-10-24: `/posttube/trending`): the ranked long
 * videos from `GET /v1/posts/trending`, with Today / Week / Month narrowing
 * on the client by when each went live — the route has no period
 * (post-service internal/http/handler.go:2674). Week first, as on the web.
 */
@HiltViewModel
class TrendingViewModel @Inject constructor(
    discovery: DiscoveryRepository,
    urlResolver: MediaUrlResolver,
    follows: FollowGraph,
    queue: TubeQueue,
    engagement: EngagementStore,
) : DiscoveryVideosViewModel(urlResolver, follows, queue, engagement) {

    val pager = DiscoveryPager(viewModelScope, "trending") { cursor -> discovery.trendingPage(cursor) }

    private val _period = MutableStateFlow(TrendingPeriod.WEEK)
    val period: StateFlow<TrendingPeriod> = _period.asStateFlow()

    /** The loaded rows narrowed to the period, the server's rank kept; the clock is read on each change. */
    val shown: StateFlow<List<FeedItem>> = combine(pager.state, _period) { state, period ->
        filterByPeriod(state.items, period, System.currentTimeMillis())
    }.stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    init {
        pager.start()
    }

    fun selectPeriod(period: TrendingPeriod) {
        _period.value = period
    }
}

/** Topics (web `/posttube/topics`): every long-video subject, one page each. */
@HiltViewModel
class TopicsViewModel @Inject constructor(private val discovery: DiscoveryRepository) : ViewModel() {

    private val _state = MutableStateFlow<TopicsState>(TopicsState.Loading)
    val state: StateFlow<TopicsState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = TopicsState.Loading
        viewModelScope.launch {
            _state.value = when (val result = discovery.topics(TopicKind.LONG)) {
                is AppResult.Success -> TopicsState.Ready(result.data)
                is AppResult.Failure -> TopicsState.Failed(discoveryFailure(result.error, "topics").message)
            }
        }
    }
}

sealed interface TopicsState {
    data object Loading : TopicsState
    data class Ready(val topics: List<Topic>) : TopicsState
    data class Failed(val message: String) : TopicsState
}

/**
 * One topic's videos (web `/posttube/topics/:slug`): `GET /v1/feed/videos
 * ?category=<slug>&sort=recent|popular` (feed-service
 * internal/http/handler.go:364). A sort change starts over from the top.
 */
@HiltViewModel
class TopicViewModel @Inject constructor(
    private val discovery: DiscoveryRepository,
    urlResolver: MediaUrlResolver,
    follows: FollowGraph,
    queue: TubeQueue,
    engagement: EngagementStore,
    savedStateHandle: SavedStateHandle,
) : DiscoveryVideosViewModel(urlResolver, follows, queue, engagement) {

    private val route = savedStateHandle.toRoute<TubeTopicRoute>()
    val slug: String = route.slug
    val label: String = route.label.ifBlank { route.slug }

    private val _sort = MutableStateFlow(TopicSort.RECENT)
    val sort: StateFlow<TopicSort> = _sort.asStateFlow()

    val pager = DiscoveryPager(viewModelScope, "this topic") { cursor -> discovery.topicPage(slug, _sort.value, cursor) }

    init {
        pager.start()
    }

    fun selectSort(sort: TopicSort) {
        if (sort == _sort.value) return
        _sort.value = sort
        pager.start()
    }
}
