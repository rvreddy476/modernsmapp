package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.ui.BlurHashImage
import com.us.android.feature.tube.ui.home.Thumbnail
import com.us.android.feature.tube.ui.home.displayTitle
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.videoMetaLine

/**
 * Over the last frame while the next episode counts down (founder,
 * 2026-09-12): which episode, the seconds left, and the two answers. The
 * scrim swallows taps so a stray one does not reach the controls under it;
 * Cancel leaves the viewer on the end screen, Play now skips the wait.
 */
@Composable
fun NextEpisodeCountdown(
    countdown: Countdown,
    onCancel: () -> Unit,
    onPlayNow: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = SCRIM_ALPHA))
            .pointerInput(Unit) { detectTapGestures { } }
            .testTag("watch_countdown"),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier.padding(UsTheme.spacing.xl),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            Text(
                text = "Up next · Episode ${countdown.next.episodeNum}",
                style = MaterialTheme.typography.labelLarge,
                color = Color.White.copy(alpha = MUTED_ALPHA),
            )
            if (countdown.next.title.isNotBlank()) {
                Text(
                    text = countdown.next.title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = Color.White,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                text = "${countdown.secondsLeft}",
                fontSize = SECONDS_SIZE,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                modifier = Modifier
                    .padding(vertical = UsTheme.spacing.s)
                    .semantics { contentDescription = "Plays in ${countdown.secondsLeft} seconds" }
                    .testTag("watch_countdown_seconds"),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                Pill(
                    label = "Cancel",
                    icon = null,
                    filled = false,
                    onClick = onCancel,
                    tag = "watch_countdown_cancel",
                )
                Pill(
                    label = "Play now",
                    icon = UsIcons.Play,
                    filled = true,
                    onClick = onPlayNow,
                    tag = "watch_countdown_play",
                )
            }
        }
    }
}

/**
 * The end of a video that goes nowhere on its own: Replay, and the first
 * few "Up next" recommendations. Rows, not a countdown, because the browse
 * list is a suggestion and the viewer picks from it or leaves.
 */
@Composable
fun WatchEndScreen(
    upNext: List<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onReplay: () -> Unit,
    onOpen: (FeedItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black.copy(alpha = SCRIM_ALPHA))
            .pointerInput(Unit) { detectTapGestures { } }
            .testTag("watch_end_screen"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            ReplayRow(onClick = onReplay)
            if (upNext.isNotEmpty()) {
                Text(
                    text = "Up next",
                    style = MaterialTheme.typography.labelLarge,
                    color = Color.White.copy(alpha = MUTED_ALPHA),
                    modifier = Modifier.padding(top = UsTheme.spacing.s),
                )
                upNext.take(END_SCREEN_ROWS).forEach { item ->
                    EndRow(item = item, thumb = thumbFor(item), onClick = { onOpen(item) })
                }
            }
        }
    }
}

@Composable
private fun ReplayRow(onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Replay"
            }
            .padding(vertical = UsTheme.spacing.xs)
            .testTag("watch_end_replay"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Box(
            modifier = Modifier
                .size(REPLAY_DISC)
                .clip(CircleShape)
                .background(Color.White.copy(alpha = DISC_ALPHA)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.RotateCcw,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(REPLAY_GLYPH),
            )
        }
        Text(
            text = "Replay",
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
        )
    }
}

/** A recommendation, compact enough that three fit under Replay in a 16:9 player. */
@Composable
private fun EndRow(item: FeedItem, thumb: VideoThumb, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Play ${item.displayTitle}"
            }
            .padding(vertical = UsTheme.spacing.xs)
            .testTag("watch_end_row:${item.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Thumbnail(
            thumb = thumb,
            shape = RoundedCornerShape(UsTheme.radii.medium),
            modifier = Modifier.width(END_THUMB_WIDTH),
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = item.displayTitle,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = Color.White,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = videoMetaLine(item.creatorName, item.createdAt, item.counts.views),
                style = MaterialTheme.typography.bodySmall,
                color = Color.White.copy(alpha = MUTED_ALPHA),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** A pill button on the scrim: filled ember for the answer we expect, outlined for the other. */
@Composable
private fun Pill(label: String, icon: ImageVector?, filled: Boolean, onClick: () -> Unit, tag: String) {
    val fill = if (filled) UsTheme.extended.accentSolid else Color.Transparent
    Row(
        modifier = Modifier
            .clip(CircleShape)
            .background(fill)
            .border(width = 1.dp, color = if (filled) fill else Color.White.copy(alpha = MUTED_ALPHA), shape = CircleShape)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                role = Role.Button,
                onClick = onClick,
            )
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        if (icon != null) {
            Icon(imageVector = icon, contentDescription = null, tint = Color.White, modifier = Modifier.size(PILL_GLYPH))
        }
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
        )
    }
}

/**
 * The video's cover over the surface once the video has ended: the wash
 * first, the still fitted whole over it. Drawn under the countdown and
 * the end screen so what they dim is always a picture, never the black a
 * re-created surface shows (see PlayerOrState in WatchScreen). Fit, not
 * crop, because in fullscreen the box is the whole screen and a cropped
 * cover would lose its edges to a 20:9 display. Swallows nothing: the
 * overlays above it already do.
 */
@Composable
fun EndedPoster(thumb: VideoThumb, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black)
            .testTag("watch_ended_poster"),
    ) {
        BlurHashImage(hash = thumb.blurhash, modifier = Modifier.fillMaxSize())
        thumb.url?.let { url ->
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

private const val END_SCREEN_ROWS = 3
private const val SCRIM_ALPHA = 0.72f
private const val MUTED_ALPHA = 0.7f
private const val DISC_ALPHA = 0.2f
private val SECONDS_SIZE = 40.sp
private val REPLAY_DISC = 40.dp
private val REPLAY_GLYPH = 20.dp
private val PILL_GLYPH = 16.dp
private val END_THUMB_WIDTH = 96.dp
