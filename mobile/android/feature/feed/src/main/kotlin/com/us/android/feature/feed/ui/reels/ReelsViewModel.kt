package com.us.android.feature.feed.ui.reels

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.analytics.AnalyticsEventType
import com.us.android.core.analytics.AnalyticsRecorder
import com.us.android.core.analytics.AnalyticsSurface
import com.us.android.core.analytics.PlayEndReason
import com.us.android.core.analytics.PlayStartMethod
import com.us.android.core.analytics.VideoWatchTracker
import com.us.android.core.analytics.WatchProbe
import com.us.android.core.analytics.WatchSession
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.ReelsSoundStore
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.SoundsRepository
import com.us.android.core.feed.data.SubscriptionGraph
import com.us.android.core.feed.data.hides
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.feed.data.soundRefusalMessage
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.feed.offline.OfflineCopy
import com.us.android.core.feed.offline.OfflineLibrary
import com.us.android.core.feed.offline.OfflineState
import com.us.android.core.feed.offline.playback
import com.us.android.core.feed.offline.posterModel
import com.us.android.core.feed.offline.soundTrack
import com.us.android.core.feed.offline.toFeedItem
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.SoundEntry
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.playsInReels
import com.us.android.core.media.sound.SoundTrack
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.ReelSound
import com.us.android.core.model.canUseSound
import com.us.android.core.ui.UsReelQuality
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
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
/**
 * Whether this row belongs in the Reels feed at all (founder, 2026-09-06:
 * a video over five minutes "should not appear in the reels section").
 *
 * The client refuses to POST a long capture as a reel — `videoGate` in
 * `:feature:post` disables Post for it — but that only covers what THIS
 * build creates. `/v1/feed/reels` can still hand back a post an older
 * client, or a publish whose length could not be probed, tagged `flick`.
 * So the length is judged again here, from the transcode's own
 * `duration_ms`: the longest video on the row, since a reel carries one.
 *
 * Pure and internal so the rule is a table test without a repository.
 */
internal fun FeedItem.belongsInReels(): Boolean =
    playsInReels(feedContentType, media.maxOfOrNull { it.durationMs } ?: 0L)

fun entryPage(postId: String, headId: String?, rankedIds: List<String>): Int? {
    if (headId == postId) return 0
    val index = rankedIds.indexOf(postId)
    if (index < 0) return null
    return index + if (headId != null) 1 else 0
}

/**
 * Whether Reels is silent, from the viewer's stored choice of sound.
 *
 * founder, 2026-09-30: reels open MUTED, like the web, and sound is on only
 * once the viewer has turned it on. A choice that has not been read yet
 * (null) is muted too: a reel may start a beat late, never loud.
 */
fun reelsMuted(soundOn: Boolean?): Boolean = soundOn != true

/**
 * Whether the settled reel may start. Not while the viewer holds it paused,
 * and not before the viewer's choice of sound is known: the first reel waits
 * for it, so someone who chose sound never hears a muted reel flip on
 * (founder, 2026-09-30).
 */
fun reelMayPlay(paused: Boolean, soundChoiceRead: Boolean): Boolean = !paused && soundChoiceRead

/**
 * A pager page as the rank analytics reports: 1-based, so the first reel is
 * position 1. Null when the page is not known, or is not a page at all.
 */
fun reelPosition(page: Int?): Int? = page?.takeIf { it >= 0 }?.plus(1)

@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity. One function per thing the screen can ask, as
// the reel publish ViewModel has: the count is the surface's, not a smell.
@Suppress("LongParameterList", "TooManyFunctions")
class ReelsViewModel @Inject constructor(
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    private val follows: FollowGraph,
    private val subscriptions: SubscriptionGraph,
    private val reelsEntry: ReelsEntry,
    private val watchTracker: VideoWatchTracker,
    private val analytics: AnalyticsRecorder,
    private val sounds: SoundsRepository,
    private val soundEntry: SoundEntry,
    private val soundStore: ReelsSoundStore,
    /** Offline copies (2026-10-02): a stored reel plays from the device, and opens with no network. */
    private val offline: OfflineLibrary,
    hidden: HiddenPosts,
) : ViewModel() {

    /**
     * The analytics view for the reel on screen.
     *
     * Reels engagement always applies to the reel being watched — the rail sits
     * on top of it — so holding the settled session here is what lets a like, a
     * save, a share or a follow be attributed to the view that earned it,
     * without widening every action signature to carry a creator id the store
     * layer would only throw away.
     */
    private var watchSession: WatchSession? = null

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
        // A long video is not a reel, whatever the row is tagged
        // (founder, 2026-09-06) — see [belongsInReels]. Applied AFTER the
        // cache so a page already held from before this build is filtered too.
        .map { page -> page.filter { it.belongsInReels() } }
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
    val head: StateFlow<ReelsHead?> = combine(tracker.items, _live) { items, live ->
        // Several reels may be pending (2026-09-05); this slot shows the
        // OLDEST still in flight — the one uploading now — and the own
        // profile's grid shows them all.
        val pending = items.firstOrNull { it.isDrawable && it.preview?.kind == PublishKind.REEL }
        val preview = pending?.preview
        when {
            live != null -> ReelsHead.Live(live)
            pending == null || preview == null -> null
            else -> ReelsHead.Pending(
                creationKey = preview.creationKey,
                coverPath = preview.coverPath,
                caption = preview.caption,
                failure = (pending.state as? ReelPublishState.Failed)?.let { PendingFailure(it.message, it.retryable) },
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), null)

    init {
        // The moment the worker reports the post id, fetch the reel the server
        // made of it and let the tracker go — the pending item becomes the
        // real thing without a refresh. A long video's post is Tube's to
        // fetch; this slot leaves it alone.
        viewModelScope.launch {
            tracker.items.collect { items ->
                items.firstOrNull { it.state is ReelPublishState.Published && it.preview?.kind == PublishKind.REEL }
                    ?.let { becomeLive(it.creationKey, (it.state as ReelPublishState.Published).postId) }
            }
        }
    }

    private suspend fun becomeLive(creationKey: String, postId: String) {
        if (_live.value?.id == postId) return
        repeat(LIVE_FETCH_ATTEMPTS) { attempt ->
            when (val result = repository.post(postId)) {
                is AppResult.Success -> {
                    _live.value = result.data
                    publishActions.dismiss(creationKey)
                    return
                }
                is AppResult.Failure -> if (attempt < LIVE_FETCH_ATTEMPTS - 1) delay(LIVE_FETCH_RETRY_MILLIS)
            }
        }
        // The post exists even if this client could not read it back yet; the
        // next refresh of the ranked feed carries it. Holding a loader over a
        // finished publish would be lying about where the work is.
        publishActions.dismiss(creationKey)
    }

    fun retryPublish() = pendingKey()?.let(publishActions::retry)

    fun discardPublish() = pendingKey()?.let(publishActions::discard)

    private fun pendingKey(): String? = (head.value as? ReelsHead.Pending)?.creationKey

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
                // No network, or the post is gone from the server: a copy kept on this
                // device still opens, from what was stored with it (2026-10-02).
                is AppResult.Failure -> offline.playable(postId)?.let { copy ->
                    _live.value = copy.toFeedItem()
                    _entryTarget.value = postId
                }
            }
        }
    }

    /** The pager is on the entry's reel; nothing more to move to. */
    fun onEntryShown() {
        _entryTarget.value = null
    }

    private val _muted = MutableStateFlow(reelsMuted(soundOn = null))

    /**
     * Whether Reels is silent.
     *
     * founder, 2026-09-30: reels open MUTED, like the web. Once the viewer
     * turns the sound on it STAYS on — across reels, across leaving and
     * re-entering Reels, and across app restarts — until they mute again:
     * "Once user makes it on on the sound keep it on." The choice is kept in
     * [ReelsSoundStore] and drives BOTH players together, the video and its
     * added sound. It replaces the 2026-09-05 decision (sound on when Reels
     * opens, a mute kept for the session only).
     *
     * Held here rather than per-player so it survives page changes and
     * player recycling — a per-player flag resets the moment the pool
     * reclaims one. Until the stored choice has been read this is muted; see
     * [soundChoiceRead].
     */
    val muted: StateFlow<Boolean> = _muted.asStateFlow()

    private val _soundChoiceRead = MutableStateFlow(false)

    /**
     * Whether the viewer's choice is known: read from the store, or made by
     * a tap that beat the read. The screen holds the first reel until it is,
     * so a viewer who chose sound never hears a muted first reel flip on
     * (founder, 2026-09-30); see [reelMayPlay].
     */
    val soundChoiceRead: StateFlow<Boolean> = _soundChoiceRead.asStateFlow()

    init {
        viewModelScope.launch {
            val stored = soundStore.soundOn.first()
            // A tap that beat the read is the newer choice, and it stands.
            if (!_soundChoiceRead.value) choose(soundOn = stored)
        }
    }

    /** The rail's speaker: the other state, at once, and kept for the next reel and the next launch. */
    fun toggleMuted() {
        val soundOn = _muted.value
        choose(soundOn)
        viewModelScope.launch { soundStore.setSoundOn(soundOn) }
    }

    private fun choose(soundOn: Boolean) {
        _muted.value = reelsMuted(soundOn)
        _soundChoiceRead.value = true
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
        // The screen is leaving composition, so the view is over. `paused`
        // rather than `ended` — the reel did not finish, the viewer left.
        endWatchAnalytics(PlayEndReason.PAUSED)
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
    fun playback(item: FeedItem): Playback? =
        reelPlayback(storedCopy(item.id)?.playback(), urlResolver.playbackFor(item))

    /**
     * The copy of a reel that plays from the device, or null when it plays
     * from the network.
     *
     * Decided ONCE per reel for this visit ([offlinePinned]): a copy that
     * finishes saving while its reel is on screen must not swap the source
     * under a playing reel (the pool re-prepares when a page's playback
     * changes, and the reel would start again from the top). A copy that is
     * removed or expires mid-visit is let go at once, and the reel goes back
     * to the network.
     */
    private fun storedCopy(postId: String): OfflineCopy? {
        val copy = offline.playable(postId)
        val pinned = offlinePinned.getOrPut(postId) { copy != null }
        return copy.takeIf { pinned }
    }

    /** Post id → whether its copy was on the device when the reel was first asked for. Main thread only. */
    private val offlinePinned = mutableMapOf<String, Boolean>()

    /**
     * What the sound player is handed for [item]: the reel's added sound,
     * from the copy stored with it when the reel plays from the device.
     */
    fun soundTrack(item: FeedItem): SoundTrack? =
        reelSoundTrack(item.soundTrack(), storedCopy(item.id)?.soundTrack())

    /** Where each reel's offline copy stands, for the ring while one is being saved. */
    val offlineState: StateFlow<OfflineState> = offline.state

    /**
     * The still frame to show before the first video frame decodes: the
     * cover the author chose when the row carries one (the cover fix,
     * 2026-09-05), else the transcode's own still — the same rule as Tube's.
     */
    fun posterUrl(item: FeedItem): String? =
        urlResolver.videoThumb(item).url ?: offline.playable(item.id)?.posterModel()

    // ── Engagement ──────────────────────────────────────────────────────

    /**
     * The same optimistic overlay the home feed layers over its rows: a
     * PagingData page cannot be edited in place, so the tap lives here and the
     * shared store does the write — a like made on a reel is already applied
     * when the same post scrolls past on Home.
     */
    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays

    fun onReact(postId: String, serverReacted: Boolean) = viewModelScope.launch {
        // Only the positive direction: the model has no "unlike" event, because
        // the engagement rate behind the content quality score counts likes
        // given rather than the running net.
        if (!serverReacted) recordEngagement(postId, AnalyticsEventType.LIKE)
        engagement.toggleReaction(postId, serverReacted)
        sayIfRefused(postId, EngagementAction.REACTION)
    }

    fun onBookmark(postId: String, serverBookmarked: Boolean) = viewModelScope.launch {
        if (!serverBookmarked) recordEngagement(postId, AnalyticsEventType.SAVE)
        engagement.toggleBookmark(postId, serverBookmarked)
        sayIfRefused(postId, EngagementAction.BOOKMARK)
    }

    private val _engagementMessage = MutableStateFlow<UsMessage?>(null)

    /**
     * A like or a save the server refused, in one line over the reel
     * (2026-10-02). The store has already put the rail's glyph back; before
     * this the rollback was silent, so a failed Save read as a button that
     * did nothing. Nothing else on this screen showed the store's failures.
     */
    val engagementMessage: StateFlow<UsMessage?> = _engagementMessage.asStateFlow()

    fun dismissEngagementMessage() {
        _engagementMessage.value = null
    }

    /**
     * Called once the store's write has settled. The failure is taken off
     * the shared list as it is said, so the Home feed's failure bar does not
     * show a reel's refusal later.
     */
    private fun sayIfRefused(postId: String, action: EngagementAction) {
        if (engagement.failures.value.none { it.postId == postId && it.action == action }) return
        _engagementMessage.value = UsMessage(reelEngagementRefusal(action))
        engagement.clearFailure(postId, action)
    }

    /** Recorded AFTER the chooser was launched; a failed count is not the viewer's problem. */
    fun onExternalShared(postId: String) = viewModelScope.launch {
        recordEngagement(postId, AnalyticsEventType.SHARE)
        shares.recordExternalShare(postId)
    }

    // ── Sounds ──────────────────────────────────────────────────────────

    private val _soundDestination = MutableStateFlow<SoundDestination?>(null)

    /**
     * Where "use this sound" has decided to go, until the screen has gone
     * there: the reel create flow, or the sound's page. The screen navigates
     * and calls [onSoundDestinationTaken]; it is null the rest of the time.
     */
    val soundDestination: StateFlow<SoundDestination?> = _soundDestination.asStateFlow()

    private val _soundMessage = MutableStateFlow<UsMessage?>(null)

    /** Why "use this sound" was refused, in one line, by the server's code. */
    val soundMessage: StateFlow<UsMessage?> = _soundMessage.asStateFlow()

    private var soundRequest: Job? = null

    /**
     * "Use this sound" (original sounds, 2026-09-30), from the More sheet's
     * row ([SoundIntent.CREATE]) or the reel's sound line
     * ([SoundIntent.PAGE]).
     *
     * A reel that already plays an added sound offers that sound and the
     * server is not asked. One that plays its own audio has its sound made
     * on first use, and a refusal is shown in one line. One request at a
     * time: a second tap while the first is on the wire is the same wish.
     */
    fun onUseSound(item: FeedItem, intent: SoundIntent) {
        if (!item.canUseSound(item.isOwnedBy(ownUserId))) return
        when (val step = nextSoundStep(item)) {
            is SoundStep.Open -> openSound(step.sound, intent)
            is SoundStep.Resolve -> {
                if (soundRequest?.isActive == true) return
                soundRequest = viewModelScope.launch {
                    when (val result = sounds.useSound(step.postId)) {
                        is AppResult.Success -> openSound(result.data, intent)
                        is AppResult.Failure -> _soundMessage.value = UsMessage(result.error.soundRefusalMessage())
                    }
                }
            }
        }
    }

    private fun openSound(sound: ReelSound, intent: SoundIntent) {
        // The create flow is another feature: the sound waits for it in the holder.
        if (intent == SoundIntent.CREATE) soundEntry.choose(sound.toChosenSound())
        _soundDestination.value = soundDestination(intent, sound)
    }

    /** The screen has gone where [soundDestination] said. */
    fun onSoundDestinationTaken() {
        _soundDestination.value = null
    }

    fun dismissSoundMessage() {
        _soundMessage.value = null
    }

    // ── Follow / Subscribe ──────────────────────────────────────────────

    /** Author id → the viewer's edge; the overlay offers Follow only when [offersFollow] says so. */
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges

    /**
     * Channel id → the viewer's subscription; the overlay offers Subscribe
     * in place of Follow when the reel carries its author's channel
     * ([reelRelationship]), because a subscribe is the richer edge (follow
     * plus notify) and the one Tube rewards.
     */
    val subscriptionEdges: StateFlow<Map<String, ChannelSubscription>> = subscriptions.edges

    val ownUserId: String get() = follows.ownId

    fun onFollow(authorId: String) = viewModelScope.launch {
        // The reels follow pill sits ON the reel, so this is unambiguously
        // follow_from_content — the creator earned it with that piece of
        // content, which is exactly the distinction the event exists to draw.
        watchSession?.let { analytics.recordEngagement(AnalyticsEventType.FOLLOW_FROM_CONTENT, it) }
        follows.follow(authorId)
    }

    /**
     * Subscribe to the reel author's channel. The same analytics event as a
     * follow: the server makes the follow edge inside the subscribe, and
     * the ranking model reads a follow earned by content, whichever button
     * earned it.
     */
    fun onSubscribe(channelId: String) = viewModelScope.launch {
        watchSession?.let { analytics.recordEngagement(AnalyticsEventType.FOLLOW_FROM_CONTENT, it) }
        subscriptions.subscribe(channelId)
    }

    /**
     * The pager settled on a page: the new reel plays (a pause belongs to
     * the reel it was made on, not the one swiped to) and its author's
     * edges are made known. The follow always, because the "more" sheet
     * reads it whatever the pill says; the subscription only when the row
     * carries a channel, so a reel without one never asks the channel
     * routes for an answer that would be 404.
     */
    fun onReelShown(item: FeedItem, probe: (suspend () -> WatchProbe)? = null, page: Int? = null) {
        _paused.value = false
        viewModelScope.launch { follows.ensureKnown(listOf(item.author.id)) }
        (reelRelationship(item) as? ReelRelationship.Subscribe)?.let { subscribe ->
            viewModelScope.launch { subscriptions.ensureKnown(listOf(subscribe.ref)) }
        }
        startWatchAnalytics(item, probe, page)
    }

    /**
     * Opens the analytics view for the settled reel and closes the previous one.
     *
     * The previous reel ends as `swipe_next` — which is the whole point of the
     * distinction in the wire contract: a reel abandoned by a swipe is a very
     * different signal from one watched to the end, and the ranking model reads
     * them differently.
     *
     * [probe] is null in tests and wherever the pager has no player for the
     * page yet (a reel still transcoding). Without one there is nothing to
     * measure, so no view is opened rather than one that would report zero.
     *
     * The view says what was true when it started (2026-09-30): whether the
     * reel was muted — it used to report `false` whatever the speaker said —
     * and where in the pager it sat, as [reelPosition].
     */
    private fun startWatchAnalytics(item: FeedItem, probe: (suspend () -> WatchProbe)?, page: Int?) {
        if (probe == null) return
        watchSession?.takeIf { it.contentId != item.id }
            ?.let { watchTracker.endView(it.contentId, PlayEndReason.SWIPE_NEXT) }
        if (watchSession?.contentId == item.id) return
        watchSession = watchTracker.startView(
            contentId = item.id,
            creatorId = item.author.id,
            surface = AnalyticsSurface.REELS,
            // Reels take the LONGEST of the row's media: the same rule
            // `belongsInReels` already uses to decide the reel is a reel.
            contentDurationMs = item.media.maxOfOrNull { it.durationMs } ?: 0L,
            // The pager plays whatever it settles on; the viewer never presses
            // play. Landing here from a feed tap is still autoplay once the
            // pager owns it — `tap` describes a play button, which reels has
            // none of.
            startMethod = PlayStartMethod.AUTOPLAY,
            isMuted = muted.value,
            isAutoplay = true,
            position = reelPosition(page),
            probe = probe,
        )
    }

    private fun endWatchAnalytics(reason: PlayEndReason) {
        watchSession?.let { watchTracker.endView(it.contentId, reason) }
        watchSession = null
    }

    private fun recordEngagement(postId: String, type: String) {
        // Guarded on the id: a tap that arrives after the pager has moved on
        // must not be credited to the reel now on screen.
        watchSession?.takeIf { it.contentId == postId }
            ?.let { analytics.recordEngagement(type, it) }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L

        /** post-service can lag the worker's answer by a beat; three tries a second apart covers it. */
        const val LIVE_FETCH_ATTEMPTS = 3
        const val LIVE_FETCH_RETRY_MILLIS = 1_000L
    }
}

/** What the reel says when a rail action was refused: the action, never the transport. */
fun reelEngagementRefusal(action: EngagementAction): String = when (action) {
    EngagementAction.REACTION -> "Couldn't save your like. Try again."
    EngagementAction.BOOKMARK -> "Couldn't save this reel. Try again."
    EngagementAction.REPOST -> "Couldn't repost that. Try again."
}
