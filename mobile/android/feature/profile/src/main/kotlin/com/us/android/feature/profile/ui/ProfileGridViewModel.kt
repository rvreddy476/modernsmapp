package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import com.us.android.core.feed.data.AuthorPostSort
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.feed.data.credits.CreditsRepository
import com.us.android.core.media.FeedEntry
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.TubeEntry
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.PublishSchedule
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishItem
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.model.FeedItem
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The tabs of a profile's media grid, in the order they are drawn.
 *
 * The first three are the AUTHOR's posts by content type, read from
 * `GET /v1/posts/by-author/:id` and sortable ([sortable]). [SAVED] is the
 * VIEWER's bookmarks (`GET /v1/posts/bookmarks`) and [LIKED] the reels they
 * liked (`GET /v1/reels/liked`, 2026-10-17), so both exist on the viewer's
 * own profile only ([gridTabsFor]); neither carries a content type of its
 * own nor has a server sort.
 */
enum class ProfileGridTab(val label: String, val contentType: String) {
    POSTS("Posts", "post"),
    REELS("Reels", "flick"),
    VIDEOS("Videos", "long_video"),

    /**
     * The long videos the profile's owner is credited in and accepted (video
     * credits, 2026-10-23), on anyone's profile — offered only when there is
     * at least one ([gridTabsFor]'s `appearsIn`).
     */
    APPEARS_IN("Appears in", "long_video"),
    SAVED("Saved", ""),
    LIKED("Liked", ""),
    ;

    /** An author tab — one content type of the profile's own posts. */
    val authored: Boolean get() = this == POSTS || this == REELS || this == VIDEOS

    /** The viewer's own lists: on their own profile only. */
    val ownOnly: Boolean get() = this == SAVED || this == LIKED

    /** Whether the Latest / Popular / Oldest control applies (the by-author sort, T-21). */
    val sortable: Boolean get() = authored
}

/**
 * The grid's tabs for a viewer: the three author tabs for everyone, and
 * Saved and Liked after them on the viewer's OWN profile — bookmarks and
 * likes are private to the viewer, so nobody else's profile could show
 * them. "Appears in" after the author tabs when [appearsIn] — the profile
 * is credited in at least one video — on anyone's profile, never empty.
 * Pure, so the sets are a table test.
 */
fun gridTabsFor(isOwn: Boolean, appearsIn: Boolean = false): List<ProfileGridTab> =
    ProfileGridTab.entries.filter { tab ->
        when {
            tab.authored -> true
            tab.ownOnly -> isOwn
            else -> appearsIn
        }
    }

/**
 * One tab's pages, re-read whenever that tab's sort changes.
 *
 * `flatMapLatest` drops the old sort's pager and subscribes a new one, so
 * the new sort starts from its first page with no cursor — the server mints
 * each cursor for one sort and answers 400 `INVALID_CURSOR` to it under
 * another. `distinctUntilChanged` keeps a re-selection of the current sort
 * from throwing the loaded pages away. Generic and pure, so it is tested
 * without Paging.
 */
@OptIn(ExperimentalCoroutinesApi::class)
internal fun <T> pagesBySort(sort: Flow<AuthorPostSort>, pages: (AuthorPostSort) -> Flow<T>): Flow<T> =
    sort.distinctUntilChanged().flatMapLatest(pages)

/**
 * A video the viewer is posting, as a tile at the head of its tab
 * (founder, 2026-09-05): the chosen cover with a ring in the middle — a
 * sweep and the percent while the bytes go up, a spin while the server
 * works — a clock when it is scheduled, and, when it stops, the reason
 * with Retry / Discard (or "Create channel").
 */
data class PendingVideoTile(
    val creationKey: String,
    val coverPath: String?,
    val title: String,
    val state: ReelPublishState,
    val tab: ProfileGridTab,
    /** RFC 3339 when the post is scheduled; the tile wears a clock. */
    val publishAt: String? = null,
) {
    val failure: ReelPublishState.Failed? get() = state as? ReelPublishState.Failed

    /** "Scheduled · 6 Sep 18:30", when there is a schedule to say. */
    val scheduleLabel: String? get() = PublishSchedule.tileLabel(publishAt)

    /** The failure sheet's title: a photo post is not a video (2026-10-19). */
    val failureTitle: String
        get() = if (tab == ProfileGridTab.POSTS) "Couldn't post your photos" else "Couldn't post your video"
}

/** A post the server is holding until its `publish_at` (2026-09-05): drawn with a clock until then. */
data class ScheduledTile(val item: FeedItem, val tab: ProfileGridTab) {
    val label: String get() = PublishSchedule.tileLabel(item.publishAt) ?: "Scheduled"
}

/**
 * Which tab a pending publish belongs on: a long video posts to Videos; a
 * reel — since the studio (2026-09-05) lands on the own profile — to
 * Reels; a photo post to Posts, since the photo studio now lands here too
 * (founder, 2026-09-06). Pure, so the placement is a table test.
 */
fun pendingTabFor(kind: PublishKind?): ProfileGridTab? = when (kind) {
    PublishKind.LONG -> ProfileGridTab.VIDEOS
    PublishKind.REEL -> ProfileGridTab.REELS
    PublishKind.PHOTO -> ProfileGridTab.POSTS
    null -> null
}

/**
 * "Uploaded successfully" — the green moment at the end of a publish
 * (founder, 2026-09-06: "once uploaded we show a green message, uploaded
 * successfully. No OK button needed — just show the message, it disappears,
 * and then go to that post, video or reel").
 *
 * [tab] is only carried so a reader of this state knows WHAT finished; the
 * hand-off itself is a separate one-shot event, because the banner is
 * something the profile draws and the navigation is something the shell
 * does.
 */
data class PublishSuccess(val tab: ProfileGridTab) {

    /** What the banner says. One sentence, no control — it takes itself away. */
    val message: String get() = "Uploaded successfully"
}

/** Which tab a scheduled post belongs on, by its content type; null for a kind the grid has no tab for. */
fun scheduledTabFor(contentType: String): ProfileGridTab? =
    ProfileGridTab.entries.firstOrNull { it.authored && it.contentType == contentType }

/**
 * The profile's media grid (2026-09-05): a user's posts, reels and long
 * videos as three paged lists through the feed seam, and — on the viewer's
 * own profile — the videos they are posting, first, each with its own
 * ring, then the posts the server is holding for a later `publish_at`.
 *
 * The publish tracker is process-wide and holds a QUEUE; this ViewModel
 * reads it only on the own profile, and lets go of each finished publish
 * once its tab has refreshed to carry the real post.
 */
@HiltViewModel
// Constructor injection of the grid's collaborators: three paged lists, the
// publish queue, and the two tab-root hand-offs. A wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ProfileGridViewModel @Inject constructor(
    private val videos: VideoFeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    private val feedEntry: FeedEntry,
    private val reelsEntry: ReelsEntry,
    private val tubeEntry: TubeEntry,
    follows: FollowGraph,
    savedStateHandle: SavedStateHandle,
    private val credits: CreditsRepository,
) : ViewModel() {

    private val ownId: String = follows.ownId

    /** The profile's owner: the route's id, or the viewer on the Me tab. */
    val userId: String = savedStateHandle.get<String>(USER_ID_KEY) ?: ownId

    val isOwn: Boolean = userId.isNotBlank() && userId == ownId

    private val _tab = MutableStateFlow(ProfileGridTab.POSTS)
    val tab: StateFlow<ProfileGridTab> = _tab

    /** A nudge, per tab, for its list to reload once a pending post is published. */
    private val _reloads = MutableStateFlow<Map<ProfileGridTab, Int>>(emptyMap())
    val reloads: StateFlow<Map<ProfileGridTab, Int>> = _reloads

    /**
     * "Your post is live, here is where it lives." One event per finished
     * publish, replayed to nobody: a buffered SharedFlow rather than a state,
     * because arriving at the feed is something that HAPPENS ONCE — a state
     * would fire again on every recomposition and every return to the profile.
     */
    private val _published = MutableSharedFlow<ProfileGridTab>(extraBufferCapacity = PUBLISHED_BUFFER)
    val published: SharedFlow<ProfileGridTab> = _published.asSharedFlow()

    private val _success = MutableStateFlow<PublishSuccess?>(null)

    /**
     * The green "Uploaded successfully" message, while it is up. A STATE,
     * unlike [published]: the banner is a thing that is on screen for a
     * while and must survive a recomposition, where arriving at a feed is a
     * thing that happens once. See [succeedThenHandOff] for the timing.
     */
    val success: StateFlow<PublishSuccess?> = _success

    private val _tabs = MutableStateFlow(gridTabsFor(isOwn))

    /**
     * The tabs this viewer gets — Saved and Liked only on their own profile,
     * "Appears in" once it is known to hold something (video credits,
     * 2026-10-23).
     */
    val tabs: StateFlow<List<ProfileGridTab>> = _tabs

    /** The long videos this profile is credited in and accepted, newest first. */
    val appearances: Flow<PagingData<FeedItem>> =
        if (userId.isBlank()) flowOf(PagingData.empty()) else credits.appearances(userId).cachedIn(viewModelScope)

    /** Asks whether "Appears in" would hold anything; again on every pull, so a new acceptance shows the tab. */
    fun checkAppearances() {
        if (userId.isBlank()) return
        viewModelScope.launch {
            val any = credits.hasAppearances(userId)
            _tabs.value = gridTabsFor(isOwn, appearsIn = any)
            if (!any && _tab.value == ProfileGridTab.APPEARS_IN) _tab.value = ProfileGridTab.POSTS
        }
    }

    private val _sorts = MutableStateFlow<Map<ProfileGridTab, AuthorPostSort>>(emptyMap())

    /**
     * Each author tab's sort (T-21); a tab absent from the map is on
     * [AuthorPostSort.LATEST]. Per tab, so re-sorting Reels leaves the Posts
     * pages where they were.
     */
    val sorts: StateFlow<Map<ProfileGridTab, AuthorPostSort>> = _sorts

    val posts: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.POSTS)
    val reels: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.REELS)
    val longVideos: Flow<PagingData<FeedItem>> = paged(ProfileGridTab.VIDEOS)

    /** The viewer's own bookmarks; never requested on anyone else's profile. */
    val saved: Flow<PagingData<FeedItem>> =
        if (isOwn) videos.savedPosts().cachedIn(viewModelScope) else flowOf(PagingData.empty())

    /** The reels the viewer liked, newest like first; never requested on anyone else's profile. */
    val liked: Flow<PagingData<FeedItem>> =
        if (isOwn) videos.likedReels().cachedIn(viewModelScope) else flowOf(PagingData.empty())

    /** The viewer's own pending videos, oldest first, each on its tab; empty on anyone else's profile. */
    val pending: StateFlow<List<PendingVideoTile>> = tracker.items
        .map { items -> if (isOwn) items.mapNotNull { it.toTile() } else emptyList() }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), emptyList())

    private val _scheduled = MutableStateFlow<List<ScheduledTile>>(emptyList())

    /** The viewer's own scheduled posts, soonest first; empty on anyone else's profile. */
    val scheduled: StateFlow<List<ScheduledTile>> = _scheduled

    private var seenKeys: Set<String> = emptySet()

    init {
        checkAppearances()
        if (isOwn) {
            refreshScheduled()
            viewModelScope.launch { tracker.items.collect { items -> onQueueChanged(items) } }
        }
    }

    /**
     * A pending publish arriving switches the grid to where it will show.
     * Published: reload that tab so the real post lands (or the scheduled
     * list, when it was scheduled), ASK THE SHELL TO CARRY THE VIEWER ON to
     * where the post now lives, then let the tracker go so the pending tile
     * leaves.
     *
     * The hand-off is an event rather than a read of the tracker by the
     * destination, because this ViewModel dismisses the tracker entry on the
     * same pass — a destination that is not composed yet would find nothing
     * left to read. The post id travels in [FeedEntry] / [ReelsEntry] for the
     * same reason the feed-to-Reels tap does: a tab root is restored, not
     * pushed, so the navigation itself carries no argument.
     *
     * A SCHEDULED post is not carried anywhere. It does not exist on any feed
     * yet, so the viewer stays on the profile where its clock tile is.
     */
    private fun onQueueChanged(items: List<ReelPublishItem>) {
        val tiles = items.mapNotNull { it.toTile() }
        val fresh = tiles.filter { it.creationKey !in seenKeys }
        seenKeys = tiles.mapTo(mutableSetOf()) { it.creationKey }
        fresh.lastOrNull()?.let { _tab.value = it.tab }
        items.filter { it.state is ReelPublishState.Published }.forEach { item ->
            val postId = (item.state as ReelPublishState.Published).postId
            item.toTile()?.let { tile ->
                if (tile.publishAt != null) {
                    refreshScheduled()
                } else {
                    reload(tile.tab)
                    succeedThenHandOff(tile.tab, postId)
                }
            }
            publishActions.dismiss(item.creationKey)
        }
    }

    /**
     * The end of the journey, as ONE moment (founder, 2026-09-06).
     *
     * The green message goes up, stays [SUCCESS_MILLIS], and then — in the
     * same step, not a second beat later — takes itself away AND hands the
     * viewer on. Two reasons for that order rather than navigating first and
     * showing the message on the destination: the message is about the
     * upload the viewer was WATCHING here, so it belongs where they watched
     * it; and a banner that outlived the navigation would arrive on the feed
     * as a leftover from the previous screen.
     *
     * 1.8 seconds is the number. Long enough to read three words without
     * hurrying — comfortably past the ~1.2 s a short banner needs to be seen
     * and read — and short enough that a viewer who already knows what it
     * says is not held on a screen they are done with. Material's shortest
     * snackbar is four seconds, which is right for a message you might act
     * on and much too long for one that is followed by a jump.
     */
    private fun succeedThenHandOff(tab: ProfileGridTab, postId: String) {
        if (postId.isBlank()) return
        _success.value = PublishSuccess(tab)
        viewModelScope.launch {
            delay(SUCCESS_MILLIS)
            _success.value = null
            handOff(tab, postId)
        }
    }

    /**
     * Where a finished publish takes the viewer: the post is pinned on the
     * surface it now lives on, and the shell is asked to go there.
     *
     * All three kinds land somewhere as of 2026-09-06 — a photo on the home
     * feed, a reel on Reels, a long video on Tube home. The long video used
     * to stay put, because Tube had no way of being told which video to open
     * with; [TubeEntry] is that way, and the founder asked for the hop.
     */
    private fun handOff(tab: ProfileGridTab, postId: String) {
        when (tab) {
            ProfileGridTab.POSTS -> feedEntry.showFirst(postId)
            ProfileGridTab.REELS -> reelsEntry.open(postId)
            ProfileGridTab.VIDEOS -> tubeEntry.showFirst(postId)
            // Nothing is ever published INTO Saved, Liked or Appears in; pendingTabFor never answers them.
            ProfileGridTab.SAVED, ProfileGridTab.LIKED, ProfileGridTab.APPEARS_IN -> return
        }
        _published.tryEmit(tab)
    }

    private fun reload(tab: ProfileGridTab) = _reloads.update { map -> map + (tab to (map[tab] ?: 0) + 1) }

    /** The scheduled list, again — after a schedule lands, or on pull. */
    fun refreshScheduled() {
        viewModelScope.launch {
            _scheduled.value = videos.scheduledPosts(SCHEDULED_LIMIT)
                .filter { it.isScheduled || !it.publishAt.isNullOrBlank() }
                .mapNotNull { item -> scheduledTabFor(item.feedContentType)?.let { ScheduledTile(item, it) } }
        }
    }

    fun select(tab: ProfileGridTab) {
        if (tab in _tabs.value) _tab.value = tab
    }

    /** The sort [tab] is on; Latest until the viewer picks another. */
    fun sortFor(tab: ProfileGridTab): AuthorPostSort = _sorts.value[tab] ?: AuthorPostSort.LATEST

    /**
     * Re-sorts one author tab. The tab's pager is replaced ([pagesBySort]),
     * so it reloads from the top under the new order; the other tabs keep
     * their pages. Saved has no server sort and ignores this.
     */
    fun selectSort(tab: ProfileGridTab, sort: AuthorPostSort) {
        if (!tab.sortable) return
        _sorts.update { it + (tab to sort) }
    }

    fun retryPublish(creationKey: String) = publishActions.retry(creationKey)

    fun discardPublish(creationKey: String) = publishActions.discard(creationKey)

    /** What a tile draws: the cover or the still, the wash, and the length for a video. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    private fun ReelPublishItem.toTile(): PendingVideoTile? {
        val preview = preview ?: return null
        if (state is ReelPublishState.Idle) return null
        val tab = pendingTabFor(preview.kind) ?: return null
        return PendingVideoTile(
            creationKey = creationKey,
            coverPath = preview.coverPath,
            title = preview.title.ifBlank { preview.caption },
            state = state,
            tab = tab,
            publishAt = preview.publishAt,
        )
    }

    private fun paged(tab: ProfileGridTab): Flow<PagingData<FeedItem>> =
        pagesBySort(_sorts.map { it[tab] ?: AuthorPostSort.LATEST }) { sort ->
            videos.authorPosts(userId, tab.contentType, sort)
        }.cachedIn(viewModelScope)

    private companion object {
        const val USER_ID_KEY = "userId"
        const val STOP_TIMEOUT_MILLIS = 5_000L
        const val SCHEDULED_LIMIT = 50

        /** Room for a queue that finishes several publishes while the screen is away. */
        const val PUBLISHED_BUFFER = 4

        /** How long the green message stays before it takes itself away — see [succeedThenHandOff]. */
        const val SUCCESS_MILLIS = 1_800L
    }
}
