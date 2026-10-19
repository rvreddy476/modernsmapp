package com.us.android.core.ui

import android.content.Context
import android.media.AudioManager
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandHorizontally
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkHorizontally
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
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
            Slider(
                value = if (muted) 0f else level,
                onValueChange = { value ->
                    level = value
                    touched++
                    audio.setStreamVolume(AudioManager.STREAM_MUSIC, (value * max).roundToInt(), 0)
                    if (asked != (value == 0f)) {
                        asked = value == 0f
                        onToggleMute()
                    }
                },
                colors = SliderDefaults.colors(
                    thumbColor = UsTheme.extended.onMedia,
                    activeTrackColor = UsTheme.extended.onMedia,
                    inactiveTrackColor = UsTheme.extended.onMedia.copy(alpha = VOLUME_TRACK_ALPHA),
                ),
                modifier = Modifier
                    .width(VOLUME_SLIDER_WIDTH)
                    .padding(end = UsTheme.spacing.l)
                    .semantics { contentDescription = "Volume" }
                    .testTag("$tagPrefix:slider"),
            )
        }
    }
}

private val VOLUME_HEIGHT = 36.dp
private val VOLUME_GLYPH = 20.dp
private val VOLUME_SLIDER_WIDTH = 132.dp
private const val VOLUME_GROUND_ALPHA = 0.4f
private const val VOLUME_TRACK_ALPHA = 0.3f
private const val VOLUME_OPEN_MILLIS = 3_000L
