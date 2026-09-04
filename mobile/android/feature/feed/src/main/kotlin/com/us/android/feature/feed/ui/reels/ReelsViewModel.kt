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
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.UsReelQuality
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

/**
 * How much chrome sits over the video (founder, 2026-09-04, from the phone).
 *
 * [NORMAL] is the reel as designed: the header (the hamburger and search)
 * translucent over the top of the video, the rail, the
 * author block with Follow and the caption, and the app's bottom bar under
 * it. [FULL] takes away ONLY the two strips the app puts around the video —
 * the header and the bottom bar — and keeps the rail and the author block,
 * because those belong to the reel, not to the app. A double-tap gets there,
 * a second one comes back. Per session and per visit: Reels OPENS in normal
 * mode every time.
 */
enum class ReelsMode {
    NORMAL,
    FULL,
    ;

    /** The other mode — what a double-tap on the video does. */
    fun toggled(): ReelsMode = if (this == NORMAL) FULL else NORMAL
}

/**
 * What each mode leaves on screen. Four flags rather than one because they
 * are drawn by different owners: the header by the screen, the rail and the
 * author block by the reel page, the bottom bar by the app shell.
 */
data class ReelsChrome(
    /** The Momentum header over the top of the video. */
    val showHeader: Boolean,
    /** Like, comment, share, save, more, mute — the right rail. */
    val showRail: Boolean,
    /** Avatar, username, Follow and the caption — bottom-left. */
    val showAuthor: Boolean,
    /** The shell's bottom navigation bar. */
    val showBottomBar: Boolean,
) {
    companion object {
        /** Everything. */
        val NORMAL = ReelsChrome(showHeader = true, showRail = true, showAuthor = true, showBottomBar = true)

        /** The app's strips gone; the reel's own controls stay. */
        val FULL = ReelsChrome(showHeader = false, showRail = true, showAuthor = true, showBottomBar = false)
    }
}

/**
 * The one rule: normal shows everything; full hides the header and the
 * bottom bar and nothing else — the rail and the author block are always on.
 */
fun ReelsMode.chrome(): ReelsChrome = when (this) {
    ReelsMode.NORMAL -> ReelsChrome.NORMAL
    ReelsMode.FULL -> ReelsChrome.FULL
}

/**
 * The page a reel sits on: 0 when it is the head, else its index among the
 * ranked reels shifted past the head when there is one; null when the pager
 * does not hold it (yet). Pure, so the scroll a feed tap asks for can be
 * pinned without a pager.
 */
fun entryPage(postId: String, headId: String?, rankedIds: List<String>): Int? {
    if (headId == postId) return 0
    val index = rankedIds.indexOf(postId)
    if (index < 0) return null
    return index + if (headId != null) 1 else 0
}

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
    private val reelsEntry: ReelsEntry,
    hidden: HiddenPosts,
) : ViewModel() {

    /**
     * The reel pinned above the ranked pages, once fetched: the one this
     * session just published, or the one a feed tap sent the viewer here
     * for when the ranked pages did not already hold it ([resolveEntry]).
     */
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
     * shows nothing rather than a blank page with a loader on it. A LONG
     * video posting (Tube, 2026-09-05) is not a reel and never sits here —
     * Tube home draws that one.
     */
    val head: StateFlow<ReelsHead?> = combine(tracker.state, tracker.preview, _live) { state, preview, live ->
        when {
            live != null -> ReelsHead.Live(live)
            preview == null || preview.kind != VideoKind.REEL || state is ReelPublishState.Idle -> null
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
        // real thing without a refresh. A long video's post is Tube's to
        // fetch; this slot leaves it alone.
        viewModelScope.launch {
            tracker.state.collect { state ->
                if (state is ReelPublishState.Published && tracker.preview.value?.kind == VideoKind.REEL) {
                    becomeLive(state.postId)
                }
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

    // ── The entry from a feed ───────────────────────────────────────────

    /**
     * The post a feed tap asked Reels to open on, until Reels has taken it
     * — see [ReelsEntry]. The screen calls [resolveEntry] once it knows what
     * its pages hold.
     */
    val entry: StateFlow<String?> = reelsEntry.requested

    private val _entryTarget = MutableStateFlow<String?>(null)

    /**
     * The reel the pager should move to, once it is on a page: the entry,
     * after [resolveEntry] has found or fetched it. The screen scrolls there
     * and calls [onEntryShown]; it is null the rest of the time.
     */
    val entryTarget: StateFlow<String?> = _entryTarget.asStateFlow()

    /**
     * Takes the feed's request, given the ids of the reels the pager can
     * already show ([loadedIds], plus the head if there is one).
     *
     * Two outcomes, the founder's rule (2026-09-05): a reel already in the
     * pages is scrolled to — [entryTarget] names it and nothing is fetched;
     * one that is not is fetched by id and pinned as the head, so it shows
     * FIRST with the ranked reels after it, and the ranked page that later
     * carries it is filtered so it never appears twice. Either way the
     * request is cleared at once, so a later visit from the tab opens where
     * Reels was left, and a fetch that fails leaves nothing to scroll to —
     * the tab simply opens.
     */
    fun resolveEntry(loadedIds: Collection<String>) {
        val postId = reelsEntry.requested.value ?: return
        reelsEntry.clear()
        if (postId in loadedIds || _live.value?.id == postId) {
            _entryTarget.value = postId
            return
        }
        viewModelScope.launch {
            when (val result = repository.post(postId)) {
                is AppResult.Success -> {
                    _live.value = result.data
                    _entryTarget.value = postId
                }
                is AppResult.Failure -> Unit
            }
        }
    }

    /** The pager is on the entry's reel; nothing more to move to. */
    fun onEntryShown() {
        _entryTarget.value = null
    }

    private val _muted = MutableStateFlow(false)

    /**
     * Reels open with SOUND ON (founder, 2026-09-05) — from the tab and
     * from a feed tap alike; the feed is the silent preview, Reels is where
     * the sound is. The rail's speaker still mutes, and the choice is held
     * for the session: here rather than per-player so it survives page
     * changes and player recycling — a per-player flag resets the moment
     * the pool reclaims one.
     */
    val muted: StateFlow<Boolean> = _muted.asStateFlow()

    fun toggleMuted() {
        _muted.value = !_muted.value
    }

    private val _mode = MutableStateFlow(ReelsMode.NORMAL)

    /**
     * Normal or full mode — see [ReelsMode]. Session state, never persisted:
     * full mode is a way of watching THIS reel, not a setting, and a viewer
     * who comes back to the tab tomorrow expects the controls to be there.
     * It survives swipes (the pager keeps playing in whatever mode it is in)
     * and is reset by [resetMode] when the screen is left.
     */
    val mode: StateFlow<ReelsMode> = _mode.asStateFlow()

    /** A double-tap on the video. Never a like: a double-tap here is about the frame, not the post. */
    fun toggleMode() {
        _mode.value = _mode.value.toggled()
    }

    private val _quality = MutableStateFlow<UsReelQuality>(UsReelQuality.Auto)

    /**
     * The rendition the viewer asked for from the more sheet's Quality row:
     * the player's own choice, or one height of the HLS ladder. Held for the
     * SESSION — a viewer who picked 360p on a thin connection wants the next
     * reel at 360p too — so it survives swipes, the pool recycling players,
     * and [resetView]; only a new process starts back at Auto. Every page's
     * player applies it as it is prepared.
     */
    val quality: StateFlow<UsReelQuality> = _quality.asStateFlow()

    fun selectQuality(quality: UsReelQuality) {
        _quality.value = quality
    }

    private val _paused = MutableStateFlow(false)

    /**
     * Whether the current reel is held still. A SINGLE tap on the video
     * pauses it and a second one plays it again — the one thing a single tap
     * does here (founder, 2026-09-04). Held per screen, not per player: a
     * swipe to another reel plays it ([onReelShown] clears the pause), and
     * the pool recycles players underneath, so a per-player flag would be
     * lost with the instance.
     */
    val paused: StateFlow<Boolean> = _paused.asStateFlow()

    fun togglePaused() {
        _paused.value = !_paused.value
    }

    /**
     * Back to normal, playing. Called when the screen leaves composition — a
     * tab switch, a pushed profile, Back — so the next visit opens with its
     * header and bar and a moving reel; the shell's bar has already been
     * given back by then.
     */
    fun resetView() {
        _mode.value = ReelsMode.NORMAL
        _paused.value = false
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

    /**
     * The pager settled on a page: the new reel plays — a pause belongs to
     * the reel it was made on, not the one swiped to — and its author's edge
     * is made known.
     */
    fun onReelShown(item: FeedItem) {
        _paused.value = false
        viewModelScope.launch { follows.ensureKnown(listOf(item.author.id)) }
    }

    private companion object {
        const val VIDEO = "video"
        const val STOP_TIMEOUT_MILLIS = 5_000L

        /** post-service can lag the worker's answer by a beat; three tries a second apart covers it. */
        const val LIVE_FETCH_ATTEMPTS = 3
        const val LIVE_FETCH_RETRY_MILLIS = 1_000L
    }
}
