package com.us.android.core.media.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.media3.common.Player
import androidx.media3.common.text.CueGroup
import com.us.android.core.designsystem.theme.UsTheme

/*
 * What a video surface draws for an offline copy (2026-10-02): the marker
 * that says the frame is coming off the device, the ring while a copy is
 * being saved, and the caption line of a stored caption track. Here, in the
 * module every video surface already reads its buffering indicator from, so
 * Reels and the long video's watch screen say it the same way.
 */

/** What the marker reads, on a reel and on a long video alike. */
const val OFFLINE_COPY_LABEL = "Offline copy"

/** "Offline copy": the frame is playing from the device, not the network. */
@Composable
fun OfflineCopyBadge(modifier: Modifier = Modifier) {
    Text(
        text = OFFLINE_COPY_LABEL,
        style = MaterialTheme.typography.labelSmall,
        color = UsTheme.extended.onMedia,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.pill))
            .background(UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("offline_copy_badge"),
    )
}

/**
 * A copy being saved: a small ring and the percentage, or [waiting] in its
 * place while the fetch is held ("Waiting for Wi-Fi"). [progress] null is a
 * fetch whose length is not known yet: the ring turns instead of filling.
 */
@Composable
fun OfflineSaveRing(progress: Float?, modifier: Modifier = Modifier, waiting: String? = null) {
    val label = waiting ?: offlinePercentLabel(progress)
    Row(
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.pill))
            .background(UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .semantics { contentDescription = "Saving offline. $label" }
            .testTag("offline_save_ring"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        if (progress != null && waiting == null) {
            CircularProgressIndicator(
                progress = { progress.coerceIn(0f, 1f) },
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.mediaTrack,
                strokeWidth = RING_STROKE,
                modifier = Modifier.size(RING_SIZE),
            )
        } else if (waiting == null) {
            CircularProgressIndicator(
                color = UsTheme.extended.accentSolid,
                trackColor = UsTheme.extended.mediaTrack,
                strokeWidth = RING_STROKE,
                modifier = Modifier.size(RING_SIZE),
            )
        }
        Text(text = label, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.onMedia)
    }
}

/** "42%", or "Saving" while the length is not known. One wording for the ring and the menu row. */
fun offlinePercentLabel(progress: Float?): String =
    if (progress == null) "Saving" else "${(progress.coerceIn(0f, 1f) * PERCENT).toInt()}%"

/**
 * The caption line: what the player's text track says right now, at the
 * bottom of the frame. Nothing is drawn while no caption track is selected,
 * so this costs a surface nothing until the viewer turns captions on.
 */
@Composable
fun BoxScope.CaptionsOverlay(player: Player, modifier: Modifier = Modifier) {
    var text by remember(player) { mutableStateOf(captionText(player.currentCues)) }
    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onCues(cueGroup: CueGroup) {
                text = captionText(cueGroup)
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }
    if (text.isBlank()) return
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.onMedia,
        textAlign = TextAlign.Center,
        modifier = modifier
            .align(Alignment.BottomCenter)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = CAPTION_BOTTOM)
            .clip(RoundedCornerShape(UsTheme.radii.pill))
            .background(UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("captions_overlay"),
    )
}

private fun captionText(group: CueGroup): String =
    group.cues.mapNotNull { it.text?.toString()?.trim() }.filter { it.isNotEmpty() }.joinToString("\n")

private const val PERCENT = 100
private val RING_SIZE = 12.dp
private val RING_STROKE = 2.dp
private val CAPTION_BOTTOM = 40.dp

@Preview
@Composable
private fun OfflineBadgesPreview() {
    UsTheme {
        Box(Modifier.background(UsTheme.extended.stage).padding(UsTheme.spacing.xxl)) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                OfflineCopyBadge()
                OfflineSaveRing(progress = 0.42f)
                OfflineSaveRing(progress = null, waiting = "Waiting for Wi-Fi")
            }
        }
    }
}
