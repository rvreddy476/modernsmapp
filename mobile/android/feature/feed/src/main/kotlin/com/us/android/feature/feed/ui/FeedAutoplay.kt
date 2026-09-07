// MatchingDeclarationName: this file is the feed's autoplay — the rule, the
// derived target, the player driver and the video slot; VisibleFrame is the
// value type the rule consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.feed.ui

import androidx.annotation.OptIn
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.currentStateAsState
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_TEXTURE_VIEW
import androidx.media3.ui.compose.modifiers.resizeWithContentScale
import androidx.media3.ui.compose.state.rememberPresentationState
import androidx.paging.compose.LazyPagingItems
import coil3.compose.AsyncImage
import com.us.android.core.media.Playback
import com.us.android.core.media.ui.VideoLoadingIndicator
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.MEDIA_FRAME_ASPECT
import com.us.android.core.ui.POST_CARD_HEADER_HEIGHT
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/**
 * Feed autoplay (founder, 2026-09-05): a video post's 4:5 frame IS a player.
 *
 * The rule, in one sentence: the most visible video card on screen plays,
 * muted and looping, and every other one shows its poster. "Most visible"
 * is measured on the FRAME, not the row — a row is its header, frame,
 * actions and caption, and a long caption would otherwise keep a video
 * "visible" after its picture had left the screen. A frame counts only
 * when at least [AUTOPLAY_VISIBLE_FRACTION] of it is on screen, so a card
 * peeking in at the bottom does not steal playback from the one the reader
 * is looking at; and when nothing clears the bar, nothing plays.
 *
 * Exactly one feed player exists ([FeedPlaybackViewModel.createSilentPlayer]),
 * driven by [DriveFeedPlayer]: it is re-pointed when the most visible video
 * changes, and paused — never released mid-scroll — when the screen is not
 * resumed, when the comments sheet is up, or when the in-place viewer
 * covers the list. The feed stays muted; there is no mute control here.
 * Sound lives in Reels, which is where a single tap on the frame goes.
 */

/** One video card's 4:5 frame, in the list's coordinates. */
internal data class VisibleFrame(val postId: String, val top: Int, val bottom: Int)

/**
 * The video to play: the frame with the largest visible fraction, provided
 * that fraction is at least [threshold]; null when none clears it.
 *
 * Ties go to the FIRST frame in [frames] — the list hands them in row
 * order, so that is the one nearer the top, which is where a reader who has
 * just scrolled is looking. Strictly greater, not greater-or-equal, is what
 * makes the rule stable: two fully visible frames do not swap the player
 * back and forth as the list is nudged.
 */
internal fun mostVisibleVideo(
    frames: List<VisibleFrame>,
    viewportTop: Int,
    viewportBottom: Int,
    threshold: Float = AUTOPLAY_VISIBLE_FRACTION,
): String? {
    var best: String? = null
    var bestFraction = 0f
    for (frame in frames) {
        val fraction = visibleFraction(frame, viewportTop, viewportBottom)
        if (fraction >= threshold && fraction > bestFraction) {
            best = frame.postId
            bestFraction = fraction
        }
    }
    return best
}

/**
 * How much of [frame] is on screen, 0..1. Zero for a frame with no height,
 * which is what a row reports before it has been measured.
 */
internal fun visibleFraction(frame: VisibleFrame, viewportTop: Int, viewportBottom: Int): Float {
    val height = frame.bottom - frame.top
    if (height <= 0) return 0f
    val visible = (min(frame.bottom, viewportBottom) - max(frame.top, viewportTop)).coerceAtLeast(0)
    return visible.toFloat() / height
}

/**
 * The post the reader is ON — the card a dwell is measured against.
 *
 * The same "most visible wins, ties to the top" rule as [mostVisibleVideo],
 * applied to whole ROWS instead of video frames, so a photo, a poll and a
 * text post are all candidates. Two differences, both forced by what a row is
 * rather than by taste:
 *
 *  - The unit is the row, not the 4:5 frame. A text post has no frame, and for
 *    a caption the reading happens in the text, which is the part of the row
 *    the frame rule would have thrown away.
 *  - A row TALLER than the viewport can never be 60 % visible, so it would be
 *    permanently invisible to this rule — and a long caption is exactly the
 *    post whose dwell is most worth knowing. Such a row qualifies on the other
 *    side of the same question instead: it counts when it fills at least
 *    [DWELL_VISIBLE_FRACTION] of the VIEWPORT. Whichever fraction is larger is
 *    the one compared, so short rows and tall rows are ranked on one number.
 *
 * Exactly one post at a time, deliberately. Three short text posts fully on
 * screen for ten seconds are not thirty seconds of reading, and counting them
 * as three ten-second dwells would tell the ranker that short text posts hold
 * people three times over.
 */
internal fun mostDwelledPost(
    rows: List<VisibleFrame>,
    viewportTop: Int,
    viewportBottom: Int,
    threshold: Float = DWELL_VISIBLE_FRACTION,
): String? {
    val viewportHeight = (viewportBottom - viewportTop).coerceAtLeast(1)
    var best: String? = null
    var bestFraction = 0f
    for (row in rows) {
        val ofRow = visibleFraction(row, viewportTop, viewportBottom)
        val visiblePx = ofRow * (row.bottom - row.top)
        val ofViewport = visiblePx / viewportHeight
        val fraction = max(ofRow, ofViewport)
        if (fraction >= threshold && fraction > bestFraction) {
            best = row.postId
            bestFraction = fraction
        }
    }
    return best
}

/**
 * The id of the video card that should be playing, derived from the list's
 * layout info as it scrolls.
 *
 * Each visible row's frame is located without measuring the card: its top is
 * the row's top plus the header ([POST_CARD_HEADER_HEIGHT]), its height the
 * list's width over the 4:5 [MEDIA_FRAME_ASPECT] — every media post sits in
 * that one frame, edge to edge. Only rows with something to PLAY are
 * candidates: a video still processing with no rendition is a poster and
 * stays one.
 *
 * Rows are addressed by their KEY, not their list index — see the body for
 * why that distinction is load-bearing when a post is pinned.
 *
 * `derivedStateOf` so the list's per-frame layout changes recompose nothing
 * until the ANSWER changes — the id, which changes a handful of times per
 * screenful, not sixty times a second.
 */
@Composable
internal fun rememberAutoplayTarget(
    listState: LazyListState,
    items: LazyPagingItems<FeedItem>,
    head: FeedItem?,
    playbackFor: (FeedItem) -> Playback?,
): State<String?> {
    val headerPx = with(LocalDensity.current) { POST_CARD_HEADER_HEIGHT.roundToPx() }
    return remember(listState, items, head, playbackFor, headerPx) {
        derivedStateOf {
            val info = listState.layoutInfo
            val frameHeight = (info.viewportSize.width / MEDIA_FRAME_ASPECT).roundToInt()
            val paged = items.itemSnapshotList.items
            val frames = info.visibleItemsInfo.mapNotNull { row ->
                // Addressed by KEY, never by index. The list's rows are not all
                // paging rows: a just-published post is pinned above them, so
                // every paged row's list index is one GREATER than its paging
                // index while that pin is present. Looking up by index returned
                // the item BELOW the row being measured — which either pointed
                // the single feed player at the wrong video, or refused to play
                // a video row because the item it happened to find had nothing
                // to play. [rememberDwellTarget] avoided this from the start;
                // this is the same footing.
                val item = feedItemForKey(row.key, paged, head) ?: return@mapNotNull null
                item?.takeIf { playbackFor(it) != null }?.let {
                    val top = row.offset + headerPx
                    VisibleFrame(postId = it.id, top = top, bottom = top + frameHeight)
                }
            }
            mostVisibleVideo(frames, info.viewportStartOffset, info.viewportEndOffset)
        }
    }
}

/**
 * The id of the post the reader is currently on, for dwell measurement.
 *
 * Read from the list's own layout info like [rememberAutoplayTarget], but
 * addressed by item KEY rather than by index. The list's rows are not all
 * paging rows — a just-published post is pinned above them and the append
 * state sits below — so `layoutInfo.visibleItemsInfo[n].index` is not the
 * paging index whenever the pinned row is present, and a lookup by index
 * would attribute the reader's time to the row above the one they are on.
 * The key is the post's own id ([FeedList] sets it), which cannot drift.
 *
 * `derivedStateOf` for the same reason as autoplay: the layout changes sixty
 * times a second while the ANSWER changes a handful of times per screenful.
 */
@Composable
internal fun rememberDwellTarget(listState: LazyListState): State<String?> =
    remember(listState) {
        derivedStateOf {
            val info = listState.layoutInfo
            val rows = info.visibleItemsInfo.mapNotNull { row ->
                dwellPostId(row.key)?.let { VisibleFrame(it, row.offset, row.offset + row.size) }
            }
            mostDwelledPost(rows, info.viewportStartOffset, info.viewportEndOffset)
        }
    }

/**
 * The post id behind a feed row's key, or null for a row that is not a post.
 *
 * The list keys its paged rows by the post id, the pinned just-published row
 * by [HEAD_KEY_PREFIX] + the id, and its footer by [APPEND_KEY]. A paging row
 * whose item has not loaded falls back to an Int index key, which is not a
 * post either.
 */
internal fun dwellPostId(key: Any?): String? {
    if (key !is String) return null
    if (key == APPEND_KEY) return null
    return key.removePrefix(HEAD_KEY_PREFIX).takeIf { it.isNotEmpty() }
}

/** The pinned just-published row's key prefix; see [dwellPostId]. */
internal const val HEAD_KEY_PREFIX = "head_"

/** The list's footer row key; see [dwellPostId]. */
internal const val APPEND_KEY = "append_state"

/**
 * The feed's one player, created once per list and released with it.
 */
@Composable
internal fun rememberFeedPlayer(viewModel: FeedPlaybackViewModel): ExoPlayer {
    val player = remember(viewModel) { viewModel.createSilentPlayer() }
    DisposableEffect(player) {
        onDispose { player.release() }
    }
    return player
}

/**
 * Keeps [player] on [playback] and playing exactly when [allowed].
 *
 * Two effects on purpose. The first re-points the player only when the
 * video changes — a re-prepare costs a first frame, and toggling the
 * comments sheet must not pay it. The second flips play/pause and runs for
 * either change. Leaving the composition pauses rather than releases: the
 * list owns the player's life, this only owns its state.
 */
@Composable
internal fun DriveFeedPlayer(
    player: ExoPlayer,
    playback: Playback?,
    allowed: Boolean,
    load: (ExoPlayer, Playback) -> Unit,
) {
    LaunchedEffect(player, playback) {
        if (playback == null) {
            player.stop()
            player.clearMediaItems()
        } else {
            load(player, playback)
        }
    }
    LaunchedEffect(player, playback, allowed) {
        player.playWhenReady = allowed && playback != null
    }
    DisposableEffect(player) {
        onDispose { player.playWhenReady = false }
    }
}

/**
 * Whether the screen is RESUMED — the feed plays only then. Backgrounding,
 * a pushed profile, a tab switch: all pause the feed player through this
 * one reading rather than through a listener per surface.
 */
@Composable
internal fun rememberIsResumed(): Boolean {
    val state by LocalLifecycleOwner.current.lifecycle.currentStateAsState()
    return state.isAtLeast(Lifecycle.State.RESUMED)
}

/**
 * The video in a card's 4:5 frame: cropped to fill the way its poster is,
 * the poster over it until the first frame decodes, and a single tap that
 * goes to Reels ([onClick]) — no play/pause, no mute; the feed is a silent
 * preview, and the reel proper is one tap away.
 *
 * A TextureView rather than a SurfaceView: the frame CLIPS the crop, and a
 * SurfaceView punches through its parent's clip. The compositor cost of a
 * TextureView is paid on one card-sized surface, not a full screen.
 */
@OptIn(UnstableApi::class)
@Composable
internal fun FeedVideo(
    player: Player,
    posterUrl: String?,
    contentDescription: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val presentation = rememberPresentationState(player)
    Box(
        modifier = modifier
            .fillMaxSize()
            .clip(RectangleShape)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics { role = Role.Button }
            .testTag("feed_video"),
        contentAlignment = Alignment.Center,
    ) {
        PlayerSurface(
            player = player,
            surfaceType = SURFACE_TYPE_TEXTURE_VIEW,
            modifier = Modifier.resizeWithContentScale(ContentScale.Crop, presentation.videoSizeDp),
        )
        // The poster until the player has a frame to show, so the swap from
        // still to moving is the same picture starting to move — never a
        // black frame between the two.
        if (presentation.coverSurface && posterUrl != null) {
            AsyncImage(
                model = posterUrl,
                contentDescription = contentDescription,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        // Over the poster, deliberately. The poster alone cannot tell a
        // reader whether the video is a beat away or never coming — it is a
        // picture either way — and a feed of stills that never move is the
        // same "stuck" the reels surface was reported for. The debounce
        // means a card that starts playing promptly shows nothing at all.
        VideoLoadingIndicator(player = player, onRetry = player::prepare)
    }
}

/** A frame plays when at least this much of it is on screen. */
internal const val AUTOPLAY_VISIBLE_FRACTION = 0.6f

/**
 * A row is being read when at least this much of it — or of the viewport — is
 * on screen. The same 60 % bar autoplay uses: "the card the reader is on"
 * should not mean two different things on one screen.
 */
internal const val DWELL_VISIBLE_FRACTION = 0.6f

/**
 * The feed item a row's key stands for, or null when the row is not a post.
 *
 * Pure, and separate from [rememberAutoplayTarget], for the reason the bug it
 * replaces existed at all: the resolution used to be a one-line index lookup
 * buried inside a `derivedStateOf`, where nothing could reach it. The geometry
 * rule beside it ([mostVisibleVideo]) has always been tested; this half never
 * was, so a lookup that returned the row BELOW the one being measured survived
 * until someone read it.
 *
 * The pinned row is searched last rather than first only because the paged
 * list is the common case; the ids are unique across both.
 */
internal fun feedItemForKey(key: Any?, paged: List<FeedItem>, head: FeedItem?): FeedItem? {
    val id = dwellPostId(key) ?: return null
    return paged.firstOrNull { it.id == id } ?: head?.takeIf { it.id == id }
}
