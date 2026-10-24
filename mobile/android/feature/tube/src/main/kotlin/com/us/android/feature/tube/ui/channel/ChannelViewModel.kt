package com.us.android.feature.tube.ui.channel

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.SubscriptionGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.media.ReelsEntry
import com.us.android.core.feed.data.credits.CreditsRepository
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.model.Channel
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.NotifyOn
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.navigation.TubeChannelRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The channel page's header: the channel, or why it is not there. */
sealed interface ChannelHeaderState {
    data object Loading : ChannelHeaderState
    data class Loaded(val channel: Channel) : ChannelHeaderState

    /** The server has no channel for this user — they post no long videos, or predate channels. */
    data object Missing : ChannelHeaderState
    data class Failed(val message: String) : ChannelHeaderState
}

/**
 * The channel page's lists, in the web's order (web parity, 2026-10-24:
 * features/posttube/channel/channelModel.ts): Videos, Shorts, Live,
 * Collections, Posts, then Members and Appears in.
 */
enum class ChannelTab(val label: String) {
    VIDEOS("Videos"),

    /** The owner's reels (`content_type=flick`); a tile opens it in Reels. */
    SHORTS("Shorts"),

    /** The owner's long videos that came from a live stream (`source = live`). */
    LIVE("Live"),

    /** The owner's public collections (the viewer's own include private ones). */
    COLLECTIONS("Collections"),

    /** The owner's ordinary posts (`content_type=post`); a tile opens the post. */
    POSTS("Posts"),

    /** The owner's members-only long videos (paid memberships, 2026-10-23), while they offer a level. */
    MEMBERS("Members"),

    /** Videos the owner is credited in and accepted (video credits, 2026-10-23). */
    APPEARS_IN("Appears in"),
}

/**
 * The channel read's tab tally (post-service service/channels.go:172-174).
 * A null count is one the server did not send, which is not zero.
 */
data class ChannelTabCounts(val shorts: Int? = null, val live: Int? = null, val collections: Int? = null)

fun Channel.tabCounts(): ChannelTabCounts = ChannelTabCounts(shortCount, liveCount, collectionCount)

/**
 * The channel's tabs, in order — the web's `visibleTabs`: Videos always;
 * Shorts, Live and Collections unless the channel says it has none (a count
 * of exactly 0; an unknown count keeps the tab, and its empty state
 * answers); Posts always (the server counts none); the owner sees every one
 * of those; then Members while the owner offers a level, and Appears in
 * when it holds something. [counts] is null while the channel is still
 * being read: only Videos and the two extras are offered until it lands,
 * so tabs do not appear and vanish under the viewer. Pure.
 */
fun channelTabsFor(
    members: Boolean,
    appearsIn: Boolean,
    counts: ChannelTabCounts? = ChannelTabCounts(),
    isOwner: Boolean = false,
): List<ChannelTab> = ChannelTab.entries.filter { tab ->
    fun has(count: Int?) = counts != null && (isOwner || count != 0)
    when (tab) {
        ChannelTab.VIDEOS -> true
        ChannelTab.SHORTS -> has(counts?.shorts)
        ChannelTab.LIVE -> has(counts?.live)
        ChannelTab.COLLECTIONS -> has(counts?.collections)
        ChannelTab.POSTS -> counts != null
        ChannelTab.MEMBERS -> members
        ChannelTab.APPEARS_IN -> appearsIn
    }
}

/** The Collections tab's list: loading, the collections, or why not. */
sealed interface ChannelCollectionsState {
    data object Loading : ChannelCollectionsState
    data class Loaded(val collections: List<VideoCollection>) : ChannelCollectionsState
    data class Failed(val message: String) : ChannelCollectionsState
}

private const val LONG_VIDEO = "long_video"
private const val SHORT_FORM = "flick"
private const val PLAIN_POST = "post"
private const val OFFLINE_LINE = "You are offline. Check your connection."

/**
 * A channel inside Tube (2026-09-05; subscriptions 2026-09-12):
 * `GET v1/channels/{user_id}` for the header, the subscription edge from
 * the shared [SubscriptionGraph], and that user's long videos through the
 * same paged read the You page uses.
 *
 * Subscribe is the page's one relationship control (founder): it is
 * follow plus notify, made by the SERVER in one call, so this view model
 * never sends a follow of its own. The follow graph is still injected
 * because the "more" sheet over a video card reads it, and because the
 * server's follow lands there on its next read.
 */
@HiltViewModel
// Constructor injection of the page's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ChannelViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    videos: VideoFeedRepository,
    private val channels: ChannelRepository,
    private val urlResolver: MediaUrlResolver,
    private val queue: TubeQueue,
    private val follows: FollowGraph,
    private val subscriptions: SubscriptionGraph,
    engagement: EngagementStore,
    hidden: HiddenPosts,
    private val credits: CreditsRepository,
    private val memberships: MembershipsRepository,
    /** The Collections tab (2026-10-24): the owner's collections. */
    private val library: VideoLibraryRepository,
    /** A Shorts tile opens that reel in the Reels tab. */
    private val reelsEntry: ReelsEntry,
) : ViewModel() {

    val userId: String = savedStateHandle.toRoute<TubeChannelRoute>().userId

    private val _offersMembers = MutableStateFlow(false)

    /** The owner's members-only long videos, newest first; each a lock to a non-member. */
    val membersItems: Flow<PagingData<FeedItem>> = videos.membersOnlyPosts(userId)
        .map { page -> page.filter { it.feedContentType == LONG_VIDEO } }
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    private val _tab = MutableStateFlow(ChannelTab.VIDEOS)

    /** Which list the page shows: the channel's own videos, or the ones its owner is credited in. */
    val tab: StateFlow<ChannelTab> = _tab.asStateFlow()

    private val _offersAppearances = MutableStateFlow(false)

    /**
     * "Appears in" is offered (video credits, 2026-10-23) only when the
     * owner accepted a credit on at least one video this viewer may see —
     * an empty tab is never shown.
     */
    val offersAppearances: StateFlow<Boolean> = _offersAppearances.asStateFlow()

    /** The videos this channel's owner accepted a credit on, newest first. */
    val appearances: Flow<PagingData<FeedItem>> = credits.appearances(userId)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    private val _header = MutableStateFlow<ChannelHeaderState>(ChannelHeaderState.Loading)
    val header: StateFlow<ChannelHeaderState> = _header.asStateFlow()

    /** The tabs offered, by what the channel read said and whose channel it is ([channelTabsFor]). */
    val tabs: StateFlow<List<ChannelTab>> = combine(_offersMembers, _offersAppearances, _header) { members, appears, header ->
        channelTabsFor(
            members = members,
            appearsIn = appears,
            counts = when (header) {
                ChannelHeaderState.Loading -> null
                is ChannelHeaderState.Loaded -> header.channel.tabCounts()
                else -> ChannelTabCounts()
            },
            isOwner = ownUserId.isNotBlank() && ownUserId == userId,
        )
    }.stateIn(viewModelScope, SharingStarted.Eagerly, listOf(ChannelTab.VIDEOS))

    fun selectTab(tab: ChannelTab) {
        if (tab !in tabs.value) return
        _tab.value = tab
        if (tab == ChannelTab.COLLECTIONS && _collections.value !is ChannelCollectionsState.Loaded) loadCollections()
    }

    /** The owner's reels, newest first. */
    val shortsItems: Flow<PagingData<FeedItem>> = videos.authorPosts(userId, SHORT_FORM)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    /** The owner's live recordings, newest first. */
    val liveItems: Flow<PagingData<FeedItem>> = videos.liveVideos(userId)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    /** The owner's ordinary posts, newest first. */
    val postItems: Flow<PagingData<FeedItem>> = videos.authorPosts(userId, PLAIN_POST)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    private val _collections = MutableStateFlow<ChannelCollectionsState>(ChannelCollectionsState.Loading)
    val collections: StateFlow<ChannelCollectionsState> = _collections.asStateFlow()

    fun loadCollections() {
        _collections.value = ChannelCollectionsState.Loading
        viewModelScope.launch {
            _collections.value = when (val result = library.collections(userId)) {
                is AppResult.Success -> ChannelCollectionsState.Loaded(result.data)
                is AppResult.Failure -> ChannelCollectionsState.Failed(
                    if (result.error is AppError.NoNetwork) OFFLINE_LINE else "We couldn't load these collections.",
                )
            }
        }
    }

    /** A Shorts tile: Reels opens on that reel. */
    fun openShort(postId: String) = reelsEntry.open(postId)

    /** Asks whether there is anything to show under "Appears in"; on every load, so a pull re-checks. */
    private fun checkAppearances() {
        viewModelScope.launch {
            val any = credits.hasAppearances(userId)
            _offersAppearances.value = any
            if (!any && _tab.value == ChannelTab.APPEARS_IN) _tab.value = ChannelTab.VIDEOS
        }
        // Members (2026-10-23): while the owner offers a level; a failed read is "no".
        viewModelScope.launch {
            val offers = (memberships.levelsOf(userId) as? AppResult.Success)?.data.orEmpty().isNotEmpty()
            _offersMembers.value = offers
            if (!offers && _tab.value == ChannelTab.MEMBERS) _tab.value = ChannelTab.VIDEOS
        }
    }

    val items: Flow<PagingData<FeedItem>> = videos.ownVideos(userId)
        .cachedIn(viewModelScope)
        .combine(hidden.state) { page, set ->
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = subscriptions.ownId

    /** Whether the viewer was subscribed when the header last loaded, the baseline the count is moved from. */
    private val loadedSubscribed = MutableStateFlow(false)

    /** The viewer's subscription toward this channel; null until the graph knows. */
    val subscription: StateFlow<ChannelSubscription?> = subscriptions.edges
        .map { it[userId] }
        .stateIn(viewModelScope, SharingStarted.Eagerly, subscriptions.edges.value[userId])

    /**
     * The count the header shows: the server's number as of the last load,
     * moved by one for the viewer's own subscribe or unsubscribe since. The
     * server count already includes the viewer when they arrived subscribed,
     * so the delta is measured against the edge AT LOAD, not against zero.
     */
    val subscriberCount: StateFlow<Int> = combine(_header, subscription, loadedSubscribed) { header, edge, atLoad ->
        val base = (header as? ChannelHeaderState.Loaded)?.channel?.subscriberCount ?: 0
        val now = edge?.subscribed ?: atLoad
        (base + (if (now) 1 else 0) - (if (atLoad) 1 else 0)).coerceAtLeast(0)
    }.stateIn(viewModelScope, SharingStarted.Eagerly, 0)

    /** A subscribe, unsubscribe or bell change in flight, so the control does not take a second tap. */
    private val _subscribeBusy = MutableStateFlow(false)
    val subscribeBusy: StateFlow<Boolean> = _subscribeBusy.asStateFlow()

    init {
        load()
        viewModelScope.launch { follows.ensureKnown(listOf(userId)) }
    }

    fun load() {
        checkAppearances()
        _header.value = ChannelHeaderState.Loading
        viewModelScope.launch {
            _header.value = when (val result = channels.read(userId)) {
                is AppResult.Success -> {
                    recordSubscription(result.data.subscription)
                    ChannelHeaderState.Loaded(result.data.channel)
                }
                is AppResult.Failure -> when (result.error) {
                    is AppError.NotFound -> ChannelHeaderState.Missing
                    is AppError.NoNetwork -> ChannelHeaderState.Failed("You're offline. Check your connection.")
                    else -> ChannelHeaderState.Failed("We couldn't load this channel.")
                }
            }
        }
    }

    /**
     * The channel read carries the viewer's edge for a signed-in caller;
     * the graph records it so the button is right on the first frame. A
     * public read carries nothing, and the graph asks its own endpoint.
     */
    private suspend fun recordSubscription(edge: ChannelSubscription?) {
        if (edge != null) {
            subscriptions.record(userId, edge)
        } else {
            subscriptions.ensureKnown(listOf(userId))
        }
        loadedSubscribed.value = subscriptions.edges.value[userId]?.subscribed ?: false
    }

    fun subscribe() = relationshipChange { subscriptions.subscribe(userId) }

    fun unsubscribe() = relationshipChange { subscriptions.unsubscribe(userId) }

    /** The bell: all → none, none → all. Only meaningful while subscribed. */
    fun toggleNotify() {
        val current = subscription.value ?: return
        if (!current.subscribed) return
        val next = if (current.notifyOn == NotifyOn.ALL) NotifyOn.NONE else NotifyOn.ALL
        relationshipChange { subscriptions.setNotifyOn(userId, next) }
    }

    private fun relationshipChange(change: suspend () -> AppResult<Unit>) {
        if (_subscribeBusy.value) return
        viewModelScope.launch {
            _subscribeBusy.value = true
            change()
            _subscribeBusy.value = false
        }
    }

    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /** A tile was tapped: the channel's loaded videos are the queue. */
    fun onOpen(loaded: List<FeedItem>) = queue.set(loaded)
}
