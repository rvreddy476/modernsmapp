package com.us.android.core.ui

import android.content.Context
import android.media.AudioManager
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandHorizontally
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkHorizontally
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.progressBarRangeInfo
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.setProgress
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.delay
import kotlin.math.roundToInt

/**
 * The sound control over a video (founder, 2026-10-19: "keep the sound icon
 * at the left, and let the user adjust the volume with it"). A tap on the
 * speaker mutes or unmutes and opens a volume slider beside it; the slider
 * folds away a few seconds after the last touch.
 *
 * The slider moves the phone's MEDIA volume, the one the volume keys move:
 * on Android the device's level is the viewer's level (the sound mixer
 * plans the creator's mix under it), so there is one volume, not two.
 * Dragging up from silence unmutes; dragging to zero mutes.
 */
@Composable
fun StageVolumeControl(
    muted: Boolean,
    onToggleMute: () -> Unit,
    modifier: Modifier = Modifier,
    tagPrefix: String = "stage_volume",
) {
    val context = LocalContext.current
    val audio = remember(context) { context.getSystemService(Context.AUDIO_SERVICE) as AudioManager }
    val max = remember(audio) { audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC).coerceAtLeast(1) }
    var level by remember { mutableFloatStateOf(audio.getStreamVolume(AudioManager.STREAM_MUSIC) / max.toFloat()) }
    var open by remember { mutableStateOf(false) }
    // What this control last asked for, so two drags before the screen catches
    // up cannot toggle twice.
    var asked by remember(muted) { mutableStateOf(muted) }
    // Bumped on every touch; the fold-away waits for it to stand still.
    var touched by remember { mutableIntStateOf(0) }
    LaunchedEffect(open, touched) {
        if (open) {
            delay(VOLUME_OPEN_MILLIS)
            open = false
        }
    }

    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = modifier
            .clip(RoundedCornerShape(VOLUME_HEIGHT / 2))
            .background(UsTheme.extended.stage.copy(alpha = VOLUME_GROUND_ALPHA))
            .testTag(tagPrefix),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(VOLUME_HEIGHT)
                .clip(CircleShape)
                .clickable(role = Role.Button, onClickLabel = if (muted) "Unmute" else "Mute") {
                    onToggleMute()
                    level = audio.getStreamVolume(AudioManager.STREAM_MUSIC) / max.toFloat()
                    open = true
                    touched++
                }
                .semantics { contentDescription = if (muted) "Unmute" else "Mute" }
                .testTag("$tagPrefix:toggle"),
        ) {
            Icon(
                imageVector = if (muted || level == 0f) UsIcons.SoundOff else UsIcons.SoundOn,
                contentDescription = null,
                tint = UsTheme.extended.onMedia,
                modifier = Modifier.size(VOLUME_GLYPH),
            )
        }
        AnimatedVisibility(
            visible = open,
            enter = fadeIn() + expandHorizontally(expandFrom = Alignment.Start),
            exit = fadeOut() + shrinkHorizontally(shrinkTowards = Alignment.Start),
        ) {
            ThinVolumeBar(
                value = if (muted) 0f else level,
                onChange = { value ->
                    level = value
                    touched++
                    audio.setStreamVolume(AudioManager.STREAM_MUSIC, (value * max).roundToInt(), 0)
                    if (asked != (value == 0f)) {
                        asked = value == 0f
                        onToggleMute()
                    }
                },
                modifier = Modifier
                    .width(VOLUME_SLIDER_WIDTH)
                    .padding(end = UsTheme.spacing.l)
                    .testTag("$tagPrefix:slider"),
            )
        }
    }
}

/**
 * The volume bar, thin as on RUTUBE (founder, 2026-10-19): a 3dp track — the
 * played part white, the rest faint — and a 12dp round knob at the level.
 * Tap anywhere on it or drag along it. The touch area is the control's full
 * 36dp height, so the thin line is still easy to hit. To a screen reader it
 * is an adjustable "Volume".
 */
@Composable
private fun ThinVolumeBar(value: Float, onChange: (Float) -> Unit, modifier: Modifier = Modifier) {
    val change by rememberUpdatedState(onChange)
    val active = UsTheme.extended.onMedia
    val rest = active.copy(alpha = VOLUME_TRACK_ALPHA)
    val at = value.coerceIn(0f, 1f)
    Canvas(
        modifier = modifier
            .height(VOLUME_HEIGHT)
            .pointerInput(Unit) {
                detectTapGestures { change((it.x / size.width).coerceIn(0f, 1f)) }
            }
            .pointerInput(Unit) {
                detectHorizontalDragGestures { pointer, _ -> change((pointer.position.x / size.width).coerceIn(0f, 1f)) }
            }
            .semantics {
                contentDescription = "Volume"
                progressBarRangeInfo = ProgressBarRangeInfo(at, 0f..1f)
                setProgress { target ->
                    change(target.coerceIn(0f, 1f))
                    true
                }
            },
    ) {
        val knob = VOLUME_KNOB.toPx() / 2
        val y = size.height / 2
        val x = knob + (size.width - 2 * knob) * at
        val stroke = VOLUME_TRACK.toPx()
        drawLine(rest, Offset(knob, y), Offset(size.width - knob, y), strokeWidth = stroke, cap = StrokeCap.Round)
        drawLine(active, Offset(knob, y), Offset(x, y), strokeWidth = stroke, cap = StrokeCap.Round)
        drawCircle(active, radius = knob, center = Offset(x, y))
    }
}

private val VOLUME_HEIGHT = 36.dp
private val VOLUME_TRACK = 3.dp
private val VOLUME_KNOB = 12.dp
private val VOLUME_GLYPH = 20.dp
private val VOLUME_SLIDER_WIDTH = 132.dp
private const val VOLUME_GROUND_ALPHA = 0.4f
private const val VOLUME_TRACK_ALPHA = 0.3f
private const val VOLUME_OPEN_MILLIS = 3_000L
