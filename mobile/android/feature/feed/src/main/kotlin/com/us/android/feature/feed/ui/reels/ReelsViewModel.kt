package com.us.android.feature.feed.ui.reels

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.FollowStatus
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.FollowGraph
import com.us.android.feature.feed.data.hides
import com.us.android.feature.feed.data.playbackFor
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The slot ABOVE the ranked reels: the viewer's own reel while it posts, and
 * the real reel the moment the server has created it.
 *
 * Instant reels (founder, 2026-09-04): a reel the viewer just posted should
 * be at the top of Reels straight away, looking like the reel it is about to
 * become — its cover, full-bleed, under a round loader — and then simply BE
 * that reel once the post exists. The ranked feed will carry the post on its
 * next refresh; until then this slot is what makes the post visible at all.
 */
sealed interface ReelsHead {
    /** Still posting (or stopped). The cover the user chose, and the caption. */
    data class Pending(
        val creationKey: String,
        val coverPath: String?,
        val caption: String,
        /** Null while work is in flight; set when the publish stopped. */
        val failure: PendingFailure? = null,
    ) : ReelsHead

    /** The post exists. Rendered exactly like every other reel, and it plays. */
    data class Live(val item: FeedItem) : ReelsHead
}

data class PendingFailure(val message: String, val retryable: Boolean)

/** Which rail controls a reel shows — the author's switches, applied. */
data class ReelRailVisibility(val showComment: Boolean, val showShare: Boolean)

/**
 * The rail honours the author's per-post controls by HIDING, not disabling:
 * on a full-bleed video a greyed-out glyph reads as broken, where an absent
 * one reads as a choice. Like, save and mute are always there — nothing the
 * author sets turns them off.
 */
fun FeedPostControls.railVisibility() = ReelRailVisibility(
    showComment = !noComments,
    showShare = !hideShare,
)

@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ReelsViewModel @Inject constructor(
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    private val follows: FollowGraph,
    hidden: HiddenPosts,
) : ViewModel() {

    /** The reel the server has created for this session's publish, once fetched. */
    private val _live = MutableStateFlow<FeedItem?>(null)

    /**
     * The ranked reels surface, one cached stream: `cachedIn` replays the
     * pages across rotation rather than refetching and dropping the viewer
     * to the first reel. No For You / Following split — the founder reversed
     * that (2026-09-04); Reels is one surface, like Instagram's.
     *
     * A reel that went live this session is filtered out of the ranked page
     * once the feed carries it: the head slot already shows it, and the same
     * reel twice in a row is the one thing the slot must not produce.
     */
    val items: Flow<PagingData<FeedItem>> = repository.feed(FeedQuery.Reels)
        .cachedIn(viewModelScope)
        .combine(_live) { page, live ->
            if (live == null) page else page.filter { it.id != live.id }
        }
        .combine(hidden.state) { page, set ->
            // "Not interested" and Block from the more sheet — removed at once,
            // the same way the home feed removes them.
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    /**
     * The slot above the feed, derived from the process-wide publish tracker
     * plus the fetched reel: nothing, the pending cover, or the live reel.
     *
     * The preview is what makes a pending item drawable; a tracker state
     * without one (a restart before the controller restored the record)
     * shows nothing rather than a blank page with a loader on it.
     */
    val head: StateFlow<ReelsHead?> = combine(tracker.state, tracker.preview, _live) { state, preview, live ->
        when {
            live != null -> ReelsHead.Live(live)
            preview == null || state is ReelPublishState.Idle -> null
            else -> ReelsHead.Pending(
                creationKey = preview.creationKey,
                coverPath = preview.coverPath,
                caption = preview.caption,
                failure = (state as? ReelPublishState.Failed)?.let { PendingFailure(it.message, it.retryable) },
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), null)

    init {
        // The moment the worker reports the post id, fetch the reel the server
        // made of it and let the tracker go — the pending item becomes the
        // real thing without a refresh.
        viewModelScope.launch {
            tracker.state.collect { state ->
                if (state is ReelPublishState.Published) becomeLive(state.postId)
            }
        }
    }

    private suspend fun becomeLive(postId: String) {
        if (_live.value?.id == postId) return
        repeat(LIVE_FETCH_ATTEMPTS) { attempt ->
            when (val result = repository.post(postId)) {
                is AppResult.Success -> {
                    _live.value = result.data
                    publishActions.dismiss()
                    return
                }
                is AppResult.Failure -> if (attempt < LIVE_FETCH_ATTEMPTS - 1) delay(LIVE_FETCH_RETRY_MILLIS)
            }
        }
        // The post exists even if this client could not read it back yet; the
        // next refresh of the ranked feed carries it. Holding a loader over a
        // finished publish would be lying about where the work is.
        publishActions.dismiss()
    }

    fun retryPublish() = publishActions.retry()

    fun discardPublish() = publishActions.discard()

    private val _muted = MutableStateFlow(true)

    /**
     * Reels start muted and stay muted until the viewer says otherwise.
     *
     * Autoplaying sound in a scrolling surface is hostile in public, and every
     * platform that ships it also ships an unmute affordance. The choice is
     * held here rather than per-player so it survives page changes and player
     * recycling — a per-player flag resets the moment the pool reclaims one.
     */
    val muted: StateFlow<Boolean> = _muted.asStateFlow()

    fun toggleMuted() {
        _muted.value = !_muted.value
    }

    /**
     * What to play for an item, or null when there is nothing to play.
     *
     * Null is a real outcome, not a defect: an asset still processing with no
     * original on offer has no rendition, and the pager shows its poster
     * rather than handing the player a URL it will fail on. The selection
     * itself — the server's `playback_url`, a ready asset's `hls_url`, a
     * processing asset's original — is [playbackFor].
     */
    fun playback(item: FeedItem): Playback? = urlResolver.playbackFor(item)

    /** The still frame to show before the first video frame decodes. */
    fun posterUrl(item: FeedItem): String? =
        item.media.firstOrNull { it.kind == VIDEO }?.let { urlResolver.thumbnail(it.variants) }

    // ── Engagement ──────────────────────────────────────────────────────

    /**
     * The same optimistic overlay the home feed layers over its rows: a
     * PagingData page cannot be edited in place, so the tap lives here and the
     * shared store does the write — a like made on a reel is already applied
     * when the same post scrolls past on Home.
     */
    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays

    fun onReact(postId: String, serverReacted: Boolean) = viewModelScope.launch {
        engagement.toggleReaction(postId, serverReacted)
    }

    fun onBookmark(postId: String, serverBookmarked: Boolean) = viewModelScope.launch {
        engagement.toggleBookmark(postId, serverBookmarked)
    }

    /** Recorded AFTER the chooser was launched; a failed count is not the viewer's problem. */
    fun onExternalShared(postId: String) = viewModelScope.launch {
        shares.recordExternalShare(postId)
    }

    // ── Follow ──────────────────────────────────────────────────────────

    /** Author id → the viewer's edge; the overlay offers Follow only when [offersFollow] says so. */
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges

    val ownUserId: String get() = follows.ownId

    fun onFollow(authorId: String) = viewModelScope.launch { follows.follow(authorId) }

    /** The pager settled on a page: make sure its author's edge is known. */
    fun onReelShown(item: FeedItem) = viewModelScope.launch { follows.ensureKnown(listOf(item.author.id)) }

    private companion object {
        const val VIDEO = "video"
        const val STOP_TIMEOUT_MILLIS = 5_000L

        /** post-service can lag the worker's answer by a beat; three tries a second apart covers it. */
        const val LIVE_FETCH_ATTEMPTS = 3
        const val LIVE_FETCH_RETRY_MILLIS = 1_000L
    }
}
