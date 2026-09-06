package com.us.android.core.media.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.transformLatest

/**
 * The buffering indicator every video surface shares.
 *
 * ## WHY THIS EXISTS
 *
 * Nothing in the app listened for [Player.STATE_BUFFERING], so while Media3
 * filled its buffer a surface simply held its last frame — or black, on a
 * cold open — with no sign that anything was happening. On a slow
 * connection that is indistinguishable from a broken player, which is
 * exactly how it was reported (founder, 2026-09-06: "it is showing stuck").
 *
 * ## THE RULE
 *
 * A surface is WAITING when the viewer has asked for playback
 * (`playWhenReady`) and there are no fresh frames to show — the player is
 * buffering, or it is ready but has not drawn its first frame yet. It is
 * NOT waiting when the viewer paused it: a held frame under a play glyph is
 * a paused video, and a spinner over it would be a lie.
 *
 * Both halves matter. [Player.STATE_BUFFERING] alone misses the cold-open
 * gap — the player can reach `STATE_READY` before its surface is attached
 * and drawing, which is the black frame the complaint was actually about —
 * so [Player.Listener.onRenderedFirstFrame] closes it. Conversely
 * `onRenderedFirstFrame` alone misses every mid-playback rebuffer, because
 * it fires once per media item.
 *
 * ## WHY IT IS DEBOUNCED
 *
 * Media3 dips into `STATE_BUFFERING` briefly and constantly during healthy
 * playback — every segment boundary can do it — and a spinner that blinks
 * on each of those is worse than no spinner at all. So waiting must last
 * [VIDEO_LOADING_DELAY_MILLIS] before anything is drawn, and the indicator
 * leaves the instant frames resume.
 *
 * ## WHY IT DOES NOT SPIN FOREVER
 *
 * A dead URL and an expired presigned link both look exactly like a slow
 * network to the player: it waits, and nothing ever arrives. After
 * [VIDEO_STALL_MILLIS] of unbroken waiting the indicator says so and offers
 * a retry, rather than spinning until the viewer gives up.
 */
enum class VideoLoadState {
    /** Frames are arriving, or the viewer paused. Draw nothing. */
    NONE,

    /** Waiting long enough for a viewer to notice. Draw the spinner. */
    LOADING,

    /** Waiting long enough that it is probably not coming. Offer a way out. */
    STALLED,
}

/**
 * A quarter of a second before the spinner appears.
 *
 * Two reasons for this number rather than a rounder one. Media3 is
 * configured to start playback after 250ms of buffered media
 * (`BUFFER_FOR_PLAYBACK_MS` in `PlayerPool`), so a healthy stall is over by
 * about then and a shorter delay would draw a spinner for stalls the viewer
 * would never have felt. And 250ms is roughly where a delay stops reading as
 * "instant" to a person, so anything the viewer does notice is covered. It
 * is also the cadence the playhead already polls at on both Reels and Tube,
 * so the indicator cannot appear out of step with the progress line.
 */
const val VIDEO_LOADING_DELAY_MILLIS = 250L

/**
 * Twelve seconds of unbroken waiting is treated as a stall.
 *
 * Long enough that a genuinely slow first segment on a bad connection still
 * arrives inside it, short enough that a viewer staring at a dead link is
 * not left there. Nothing is torn down at this point — the player keeps
 * trying, and a late first frame still clears the indicator; the only change
 * is that the surface stops pretending the wait is normal.
 */
const val VIDEO_STALL_MILLIS = 12_000L

/**
 * The load state of [player], debounced, for a surface to draw.
 *
 * Registers one [Player.Listener] and removes it on dispose — the same
 * shape the quality picker's track listener uses. A null player (nothing
 * playable yet) is never loading: that surface has its own "still
 * processing" state and a spinner over it would contradict it.
 */
@Composable
fun rememberVideoLoadState(
    player: Player?,
    showAfterMillis: Long = VIDEO_LOADING_DELAY_MILLIS,
    stallAfterMillis: Long = VIDEO_STALL_MILLIS,
): VideoLoadState {
    val signal = rememberVideoSignal(player)
    val states = remember(signal, showAfterMillis, stallAfterMillis) {
        snapshotFlow { signal.value.isWaitingForFrames() }
            .videoLoadStates(showAfterMillis, stallAfterMillis)
    }
    return states.collectAsState(VideoLoadState.NONE).value
}

/**
 * The whole thing in one line: observe [player] and draw the indicator over
 * the surface. Surfaces that need the state for something else as well —
 * Tube hides its centre play button while loading — use
 * [rememberVideoLoadState] and [VideoLoadingOverlay] separately.
 */
@Composable
fun VideoLoadingIndicator(
    player: Player?,
    modifier: Modifier = Modifier,
    onRetry: (() -> Unit)? = null,
) {
    VideoLoadingOverlay(state = rememberVideoLoadState(player), modifier = modifier, onRetry = onRetry)
}

/**
 * What waiting looks like: an ember ring on a dark disc, centred over the
 * frame, fading in and out so it never snaps into place.
 *
 * Ember rather than a white Material spinner — every video surface in this
 * app is dark (Reels and Tube are black, the studio preview is a black 9:16
 * box), and white-on-black is the one combination that reads as a system
 * dialog rather than as part of the product. The disc is the same
 * black-at-55% plate the Tube controls already put behind their glyphs, so
 * the indicator is legible over a bright frame as well as a dark one.
 *
 * At [VideoLoadState.STALLED] the ring stays — the player has not given up,
 * so neither does the indicator — and a line of copy plus a Retry appear
 * under it. With no [onRetry] the copy appears alone, which is still the
 * difference between "slow" and "silently broken".
 */
@Composable
fun VideoLoadingOverlay(
    state: VideoLoadState,
    modifier: Modifier = Modifier,
    onRetry: (() -> Unit)? = null,
) {
    AnimatedVisibility(
        visible = state != VideoLoadState.NONE,
        modifier = modifier,
        enter = fadeIn(tween(FADE_MILLIS)),
        exit = fadeOut(tween(FADE_MILLIS)),
    ) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .semantics { contentDescription = LOADING_DESCRIPTION }
                .testTag(VIDEO_LOADING_TAG),
            contentAlignment = Alignment.Center,
        ) {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                Spinner()
                if (state == VideoLoadState.STALLED) StalledNote(onRetry = onRetry)
            }
        }
    }
}

@Composable
private fun Spinner() {
    Box(
        modifier = Modifier
            .size(DISC)
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PLATE_ALPHA)),
        contentAlignment = Alignment.Center,
    ) {
        CircularProgressIndicator(
            modifier = Modifier.size(RING),
            strokeWidth = RING_STROKE,
            color = UsTheme.extended.accentSolid,
            trackColor = Color.White.copy(alpha = TRACK_ALPHA),
        )
    }
}

/** The stall's copy, and the way out of it when the surface offers one. */
@Composable
private fun StalledNote(onRetry: (() -> Unit)?) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(
            text = STALLED_MESSAGE,
            style = MaterialTheme.typography.labelMedium,
            color = Color.White,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl),
        )
        if (onRetry != null) {
            Text(
                text = "Retry",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.accentSolid,
                modifier = Modifier
                    .clip(CircleShape)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        onClick = onRetry,
                    )
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
                    .semantics { role = Role.Button }
                    .testTag(VIDEO_LOADING_RETRY_TAG),
            )
        }
    }
}

// ── The observation ──────────────────────────────────────────────────────

/**
 * The player's four states, as this file cares about them.
 *
 * Its own enum rather than the raw `Player.STATE_*` ints so the rule below
 * is exhaustive at compile time and testable without an Android runtime.
 */
internal enum class PlayerPhase { IDLE, BUFFERING, READY, ENDED }

/** Everything the rule reads off a player, in one value. */
internal data class VideoSignal(
    val phase: PlayerPhase,
    val playWhenReady: Boolean,
    val renderedFirstFrame: Boolean,
)

internal fun playerPhaseOf(playbackState: Int): PlayerPhase = when (playbackState) {
    Player.STATE_BUFFERING -> PlayerPhase.BUFFERING
    Player.STATE_READY -> PlayerPhase.READY
    Player.STATE_ENDED -> PlayerPhase.ENDED
    else -> PlayerPhase.IDLE
}

/**
 * Whether the surface owes the viewer frames it cannot draw.
 *
 * `playWhenReady` gates everything: a viewer-initiated pause is not a wait,
 * and neither is a Reels neighbour prepared in the background — the pool
 * preloads those with `playWhenReady = false`, and a spinner on a page
 * nobody is looking at would flash into view on every swipe.
 *
 * [PlayerPhase.IDLE] is deliberately not waiting. An idle player has no
 * media prepared, which means it was stopped or it failed, and both of
 * those are the surface's own error or "still processing" state — a spinner
 * over either would say the opposite of what the surface says.
 */
internal fun VideoSignal.isWaitingForFrames(): Boolean = playWhenReady && when (phase) {
    PlayerPhase.BUFFERING -> true
    // Ready, but nothing on screen yet: the surface has not been attached
    // and drawn to. This is the cold open the complaint was about.
    PlayerPhase.READY -> !renderedFirstFrame
    PlayerPhase.IDLE, PlayerPhase.ENDED -> false
}

/**
 * The debounce, as a flow operator so it is testable on virtual time
 * rather than only on a device.
 *
 * Each change of the waiting flag cancels the previous timing run
 * ([transformLatest]), so waiting that ends inside [showAfterMillis] emits
 * nothing but [VideoLoadState.NONE] and the viewer sees no flicker;
 * waiting that outlasts it becomes [VideoLoadState.LOADING], and waiting
 * that outlasts [stallAfterMillis] becomes [VideoLoadState.STALLED]. The
 * hide is immediate in every case, because a surface that has resumed must
 * not keep a spinner over it for even one frame.
 */
@OptIn(ExperimentalCoroutinesApi::class)
internal fun Flow<Boolean>.videoLoadStates(
    showAfterMillis: Long,
    stallAfterMillis: Long,
): Flow<VideoLoadState> = distinctUntilChanged()
    .transformLatest { waiting ->
        emit(VideoLoadState.NONE)
        if (!waiting) return@transformLatest
        delay(showAfterMillis)
        emit(VideoLoadState.LOADING)
        delay((stallAfterMillis - showAfterMillis).coerceAtLeast(0L))
        emit(VideoLoadState.STALLED)
    }
    .distinctUntilChanged()

/**
 * [VideoSignal] for [player], kept current by one listener.
 *
 * `renderedFirstFrame` is tracked here rather than read off the player
 * because Media3 exposes no such getter. It is SEEDED from the player's
 * state at attach: a player already in `STATE_READY` when this composable
 * runs has frames on screen — that is what ready means for a video
 * renderer — and seeding it is what stops a surface that is re-composed
 * mid-playback (leaving and returning to Tube on the ViewModel's player,
 * a Reels page scrolled back to) from waiting for a first frame that was
 * drawn before anybody was listening.
 *
 * It is cleared again when the player goes idle (a stop, a re-prepare for a
 * new quality rung) or moves to another media item, because both of those
 * genuinely start over.
 */
@Composable
private fun rememberVideoSignal(player: Player?): State<VideoSignal> {
    val signal = remember(player) { mutableStateOf(initialSignal(player)) }
    DisposableEffect(player) {
        if (player == null) return@DisposableEffect onDispose { }
        var rendered = signal.value.renderedFirstFrame
        fun publish() {
            signal.value = VideoSignal(
                phase = playerPhaseOf(player.playbackState),
                playWhenReady = player.playWhenReady,
                renderedFirstFrame = rendered,
            )
        }
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_IDLE) rendered = false
                publish()
            }

            override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) = publish()

            override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
                rendered = false
                publish()
            }

            override fun onRenderedFirstFrame() {
                rendered = true
                publish()
            }
        }
        player.addListener(listener)
        // The player may already have moved on between the seed above and
        // this listener being attached; read it once more now.
        publish()
        onDispose { player.removeListener(listener) }
    }
    return signal
}

private fun initialSignal(player: Player?): VideoSignal = if (player == null) {
    VideoSignal(phase = PlayerPhase.IDLE, playWhenReady = false, renderedFirstFrame = false)
} else {
    VideoSignal(
        phase = playerPhaseOf(player.playbackState),
        playWhenReady = player.playWhenReady,
        renderedFirstFrame = player.playbackState == Player.STATE_READY,
    )
}

/** The overlay, for tests and for surfaces that need to find it. */
const val VIDEO_LOADING_TAG = "video_loading"

/** The stall's Retry, where a surface offers one. */
const val VIDEO_LOADING_RETRY_TAG = "video_loading_retry"

private const val LOADING_DESCRIPTION = "Loading video"
private const val STALLED_MESSAGE = "This is taking longer than usual."
private const val FADE_MILLIS = 150
private const val PLATE_ALPHA = 0.55f
private const val TRACK_ALPHA = 0.18f
private val DISC = 56.dp
private val RING = 28.dp
private val RING_STROKE = 3.dp
