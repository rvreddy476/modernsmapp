package com.us.android.feature.tube.ui.watch

import androidx.annotation.OptIn
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.tube.ui.formatDuration
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/** Where the playhead is, read off the player four times a second. */
data class Playhead(
    val positionMs: Long = 0L,
    val bufferedMs: Long = 0L,
    val durationMs: Long = 0L,
    val playing: Boolean = false,
) {
    /** 0..1 of the video played; 0 while the length is unknown. */
    val playedFraction: Float get() = fraction(positionMs)
    val bufferedFraction: Float get() = fraction(bufferedMs)

    private fun fraction(value: Long): Float =
        if (durationMs <= 0L || value <= 0L) 0f else (value.toDouble() / durationMs).toFloat().coerceIn(0f, 1f)
}

/** Everything the overlay can ask of the player, hoisted once so the overlay is stateless about it. */
class WatchTransport(
    val onTogglePlay: () -> Unit,
    val onSeekBy: (deltaMillis: Long) -> Unit,
    val onSeekTo: (positionMillis: Long) -> Unit,
    val onToggleFullscreen: () -> Unit,
    val onOpenSettings: () -> Unit,
    val onBack: () -> Unit,
)

/** The player's clock, polled while [polling] and held still otherwise. */
@Composable
fun rememberPlayhead(player: Player, polling: Boolean): Playhead {
    var playhead by remember(player) { mutableStateOf(Playhead()) }
    LaunchedEffect(player, polling) {
        while (polling && isActive) {
            playhead = Playhead(
                positionMs = player.currentPosition.coerceAtLeast(0L),
                bufferedMs = player.bufferedPosition.coerceAtLeast(0L),
                durationMs = player.duration.takeIf { it > 0L } ?: 0L,
                playing = player.isPlaying,
            )
            delay(POLL_MILLIS)
        }
    }
    return playhead
}

/**
 * The player with its controls over it (Tube, 2026-09-05, from YouTube):
 *
 *  - a TAP shows or hides the controls; shown ones leave on their own three
 *    seconds into playback;
 *  - a DOUBLE-tap on the left or right half skips ten seconds that way and
 *    says so for a beat ([SkipHint]);
 *  - play/pause in the centre, back and the gear along the top, and along
 *    the bottom the elapsed time, the seek bar (buffered under played, a
 *    thumb to drag) the total, and fullscreen;
 *  - with the controls hidden a two-pixel line along the bottom keeps the
 *    playhead visible ([ProgressLine]).
 */
@OptIn(UnstableApi::class)
@Composable
fun WatchPlayer(
    player: Player,
    playhead: Playhead,
    fullscreen: Boolean,
    transport: WatchTransport,
    modifier: Modifier = Modifier,
) {
    var controlsVisible by remember { mutableStateOf(true) }
    var skipHint by remember { mutableStateOf<SkipHint?>(null) }
    // Bumped on every interaction so the auto-hide restarts from the last touch.
    var touch by remember { mutableIntStateOf(0) }
    LaunchedEffect(controlsVisible, playhead.playing, touch) {
        if (controlsVisible && playhead.playing) {
            delay(AUTO_HIDE_MILLIS)
            controlsVisible = false
        }
    }
    LaunchedEffect(skipHint) {
        if (skipHint != null) {
            delay(SKIP_HINT_MILLIS)
            skipHint = null
        }
    }
    val interact: () -> Unit = { touch++ }

    Box(
        modifier = modifier
            .background(Color.Black)
            .testTag("watch_player"),
    ) {
        PlayerSurface(player = player, surfaceType = SURFACE_TYPE_SURFACE_VIEW, modifier = Modifier.fillMaxSize())
        Box(
            modifier = Modifier
                .fillMaxSize()
                .pointerInput(transport) {
                    detectTapGestures(
                        onTap = { controlsVisible = !controlsVisible },
                        onDoubleTap = { offset ->
                            val back = offset.x < size.width / 2
                            transport.onSeekBy(if (back) -SKIP_MILLIS else SKIP_MILLIS)
                            skipHint = if (back) SkipHint.BACK else SkipHint.FORWARD
                            interact()
                        },
                    )
                },
        )
        SkipHints(hint = skipHint)
        AnimatedVisibility(
            visible = controlsVisible,
            enter = fadeIn(tween(FADE_MILLIS)),
            exit = fadeOut(tween(FADE_MILLIS)),
        ) {
            Controls(playhead = playhead, fullscreen = fullscreen, transport = transport, onInteract = interact)
        }
        if (!controlsVisible) {
            ProgressLine(playhead = playhead, modifier = Modifier.align(Alignment.BottomCenter))
        }
    }
}

private enum class SkipHint { BACK, FORWARD }

/** "−10s" on the left, "+10s" on the right, for a beat after a double-tap. */
@Composable
private fun BoxScope.SkipHints(hint: SkipHint?) {
    AnimatedVisibility(
        visible = hint == SkipHint.BACK,
        modifier = Modifier
            .align(Alignment.CenterStart)
            .padding(start = SKIP_INSET),
        enter = fadeIn(tween(FADE_MILLIS)),
        exit = fadeOut(tween(FADE_MILLIS)),
    ) {
        SkipPlate(icon = UsIcons.RotateCcw, label = "−10s")
    }
    AnimatedVisibility(
        visible = hint == SkipHint.FORWARD,
        modifier = Modifier
            .align(Alignment.CenterEnd)
            .padding(end = SKIP_INSET),
        enter = fadeIn(tween(FADE_MILLIS)),
        exit = fadeOut(tween(FADE_MILLIS)),
    ) {
        SkipPlate(icon = UsIcons.RotateCw, label = "+10s")
    }
}

@Composable
private fun SkipPlate(icon: ImageVector, label: String) {
    Row(
        modifier = Modifier
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PLATE_ALPHA))
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
            .testTag("watch_skip"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = Color.White, modifier = Modifier.size(SKIP_GLYPH))
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
        )
    }
}

/** The scrim, the centre control, the top row and the bottom row — what a tap reveals. */
@Composable
private fun Controls(
    playhead: Playhead,
    fullscreen: Boolean,
    transport: WatchTransport,
    onInteract: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = SCRIM_ALPHA))
            .testTag("watch_controls"),
    ) {
        Row(
            modifier = Modifier
                .align(Alignment.TopCenter)
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.xs),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            GlyphButton(icon = UsIcons.Back, description = "Back", onClick = transport.onBack, tag = "watch_back")
            GlyphButton(
                icon = UsIcons.Settings,
                description = "Quality and speed",
                onClick = {
                    onInteract()
                    transport.onOpenSettings()
                },
                tag = "watch_settings",
            )
        }
        PlayPauseButton(
            playing = playhead.playing,
            onClick = {
                onInteract()
                transport.onTogglePlay()
            },
            modifier = Modifier.align(Alignment.Center),
        )
        BottomRow(
            playhead = playhead,
            fullscreen = fullscreen,
            transport = transport,
            onInteract = onInteract,
            modifier = Modifier.align(Alignment.BottomCenter),
        )
    }
}

@Composable
private fun BottomRow(
    playhead: Playhead,
    fullscreen: Boolean,
    transport: WatchTransport,
    onInteract: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(start = UsTheme.spacing.l, end = UsTheme.spacing.xs, bottom = UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        TimeText(text = formatDuration(playhead.positionMs), tag = "watch_elapsed")
        SeekBar(
            playhead = playhead,
            onSeekTo = {
                onInteract()
                transport.onSeekTo(it)
            },
            modifier = Modifier.weight(1f),
        )
        TimeText(text = formatDuration(playhead.durationMs), tag = "watch_total")
        GlyphButton(
            icon = if (fullscreen) UsIcons.Minimize else UsIcons.Maximize,
            description = if (fullscreen) "Exit fullscreen" else "Fullscreen",
            onClick = transport.onToggleFullscreen,
            tag = "watch_fullscreen",
        )
    }
}

@Composable
private fun TimeText(text: String, tag: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.SemiBold,
        color = Color.White,
        modifier = Modifier.testTag(tag),
    )
}

@Composable
private fun GlyphButton(icon: ImageVector, description: String, onClick: () -> Unit, tag: String) {
    IconButton(onClick = onClick, modifier = Modifier.testTag(tag)) {
        Icon(imageVector = icon, contentDescription = description, tint = Color.White)
    }
}

/** A 64dp dark disc under a 32dp play or pause glyph. No ripple: the disc is the target. */
@Composable
private fun PlayPauseButton(playing: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .size(PLAY_DISC)
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PLATE_ALPHA))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics {
                role = Role.Button
                contentDescription = if (playing) "Pause" else "Play"
            }
            .testTag("watch_play_pause"),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = if (playing) UsIcons.Pause else UsIcons.Play,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(PLAY_GLYPH),
        )
    }
}

/**
 * The seek bar: a white-at-25% track, the buffered stretch at 45% over it,
 * the ember played stretch over that, and a thumb. A tap seeks there; a
 * drag scrubs and seeks on release, drawing the finger's position rather
 * than the player's until then so the thumb follows the hand.
 */
@Composable
private fun SeekBar(playhead: Playhead, onSeekTo: (Long) -> Unit, modifier: Modifier = Modifier) {
    var scrub by remember { mutableStateOf<Float?>(null) }
    val played = scrub ?: playhead.playedFraction
    val buffered = playhead.bufferedFraction
    val track = Color.White.copy(alpha = TRACK_ALPHA)
    val loaded = Color.White.copy(alpha = BUFFERED_ALPHA)
    val accent = UsTheme.extended.accentSolid
    val duration = playhead.durationMs
    Canvas(
        modifier = modifier
            .height(SEEK_TOUCH_HEIGHT)
            .semantics { contentDescription = "Seek bar" }
            .testTag("watch_seek")
            .pointerInput(duration) {
                detectTapGestures { offset -> onSeekTo((offset.x / size.width * duration).toLong()) }
            }
            .pointerInput(duration) {
                detectHorizontalDragGestures(
                    onDragStart = { offset -> scrub = (offset.x / size.width).coerceIn(0f, 1f) },
                    onHorizontalDrag = { change, _ -> scrub = (change.position.x / size.width).coerceIn(0f, 1f) },
                    onDragEnd = {
                        scrub?.let { onSeekTo((it * duration).toLong()) }
                        scrub = null
                    },
                    onDragCancel = { scrub = null },
                )
            },
    ) {
        val y = size.height / 2
        val stroke = SEEK_STROKE.toPx()
        drawLine(track, Offset(0f, y), Offset(size.width, y), stroke, StrokeCap.Round)
        if (buffered > 0f) drawLine(loaded, Offset(0f, y), Offset(size.width * buffered, y), stroke, StrokeCap.Round)
        if (played > 0f) drawLine(accent, Offset(0f, y), Offset(size.width * played, y), stroke, StrokeCap.Round)
        drawCircle(accent, radius = SEEK_THUMB.toPx() / 2, center = Offset(size.width * played, y))
    }
}

/** The playhead as a 2dp line along the bottom edge while the controls are away. */
@Composable
private fun ProgressLine(playhead: Playhead, modifier: Modifier = Modifier) {
    val track = Color.White.copy(alpha = TRACK_ALPHA)
    val accent = UsTheme.extended.accentSolid
    val played = playhead.playedFraction
    Canvas(
        modifier = modifier
            .fillMaxWidth()
            .height(LINE_HEIGHT)
            .testTag("watch_progress_line"),
    ) {
        drawRect(track)
        if (played > 0f) drawRect(accent, size = size.copy(width = size.width * played))
    }
}

private const val POLL_MILLIS = 250L
private const val AUTO_HIDE_MILLIS = 3_000L
private const val SKIP_HINT_MILLIS = 600L
private const val SKIP_MILLIS = 10_000L
private const val FADE_MILLIS = 150
private const val SCRIM_ALPHA = 0.35f
private const val PLATE_ALPHA = 0.55f
private const val TRACK_ALPHA = 0.25f
private const val BUFFERED_ALPHA = 0.45f
private val PLAY_DISC = 64.dp
private val PLAY_GLYPH = 32.dp
private val SKIP_GLYPH = 18.dp
private val SKIP_INSET = 32.dp
private val SEEK_TOUCH_HEIGHT = 32.dp
private val SEEK_STROKE = 3.dp
private val SEEK_THUMB = 12.dp
private val LINE_HEIGHT = 2.dp
