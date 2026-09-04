package com.us.android.feature.tube.ui.home

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
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
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.ui.BlurHashImage
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.formatDuration
import com.us.android.feature.tube.ui.videoMetaLine
import java.io.File

/**
 * One video on Tube home (founder, 2026-09-05, from YouTube's list): a 16:9
 * still with the length in its corner, then the author's avatar beside the
 * title — two lines, Outfit semibold — and a quiet line of author · age ·
 * views. The whole card is one target; there is no button on it.
 */
@Composable
internal fun VideoCard(
    item: FeedItem,
    thumb: VideoThumb,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val title = item.title.ifBlank { item.text }.ifBlank { UNTITLED }
    Column(
        modifier = modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Play $title"
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("tube_card:${item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Thumbnail(thumb = thumb, coverPath = null)
        Row(
            verticalAlignment = Alignment.Top,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsAvatar(name = item.author.nameForDisplay, seed = item.author.id, size = UsAvatarSize.Post)
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                TitleText(title)
                Text(
                    text = videoMetaLine(item.author.nameForDisplay, item.createdAt, item.counts.views),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/**
 * The viewer's own long video while it posts: the chosen cover under a
 * round loader and "Publishing…", the title where it will be. A stopped
 * publish swaps the loader for one strip — "Couldn't post · Retry ·
 * Discard" — and nothing else on the card changes.
 */
@Composable
internal fun PendingVideoCard(
    head: TubeHead.Pending,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("tube_pending"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box {
            Thumbnail(thumb = VideoThumb(url = null, blurhash = "", durationMs = 0L), coverPath = head.coverPath)
            if (head.failure == null) {
                PublishingOverlay(modifier = Modifier.matchParentSize())
            }
        }
        head.failure?.let { failure ->
            PublishFailureStrip(message = failure.message, retryable = failure.retryable, onRetry, onDiscard)
        }
        TitleText(head.title.ifBlank { head.caption }.ifBlank { UNTITLED })
    }
}

/** A small row for "Up next": the still at the left, the title and its line beside it. */
@Composable
internal fun VideoRow(
    item: FeedItem,
    thumb: VideoThumb,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val title = item.title.ifBlank { item.text }.ifBlank { UNTITLED }
    Row(
        modifier = modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Play $title"
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .testTag("tube_row:${item.id}"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Thumbnail(thumb = thumb, coverPath = null, modifier = Modifier.width(ROW_THUMB_WIDTH))
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontSize = ROW_TITLE_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = TITLE_LINES,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = videoMetaLine(item.author.nameForDisplay, item.createdAt, item.counts.views),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
private fun TitleText(title: String) {
    Text(
        text = title,
        style = MaterialTheme.typography.titleMedium,
        fontSize = TITLE_SIZE,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.textPrimary,
        maxLines = TITLE_LINES,
        overflow = TextOverflow.Ellipsis,
    )
}

/**
 * The 16:9 frame: the BlurHash wash first, the still over it as it loads,
 * the length badge bottom-right. [coverPath] is a local file — the pending
 * card's cover — and wins over a URL when set.
 */
@Composable
private fun Thumbnail(thumb: VideoThumb, coverPath: String?, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(LANDSCAPE)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard),
    ) {
        BlurHashImage(hash = thumb.blurhash, modifier = Modifier.fillMaxSize())
        val model: Any? = coverPath?.let(::File) ?: thumb.url
        if (model != null) {
            AsyncImage(
                model = model,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        if (thumb.durationMs > 0L) {
            DurationBadge(
                text = formatDuration(thumb.durationMs),
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(UsTheme.spacing.m),
            )
        }
    }
}

/** `m:ss` on a dark plate, YouTube's corner badge. */
@Composable
private fun DurationBadge(text: String, modifier: Modifier = Modifier) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.SemiBold,
        color = Color.White,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(Color.Black.copy(alpha = BADGE_PLATE_ALPHA))
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs)
            .semantics { contentDescription = "Length $text" }
            .testTag("tube_duration"),
    )
}

@Composable
private fun PublishingOverlay(modifier: Modifier = Modifier) {
    Column(
        modifier = modifier.background(Color.Black.copy(alpha = OVERLAY_ALPHA)),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        CircularProgressIndicator(
            color = Color.White,
            trackColor = Color.White.copy(alpha = LOADER_TRACK_ALPHA),
            strokeWidth = LOADER_STROKE,
            modifier = Modifier
                .size(LOADER_SIZE)
                .semantics { contentDescription = "Publishing your video" },
        )
        Text(
            text = "Publishing…",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
            modifier = Modifier.padding(top = UsTheme.spacing.m),
        )
    }
}

@Composable
private fun PublishFailureStrip(
    message: String,
    retryable: Boolean,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.glassBg)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .testTag("tube_pending_failure"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "Couldn't post",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.semantics { contentDescription = "Couldn't post. $message" },
        )
        if (retryable) {
            StripDot()
            StripAction(label = "Retry", onClick = onRetry)
        }
        StripDot()
        StripAction(label = "Discard", onClick = onDiscard)
    }
}

@Composable
private fun StripDot() {
    Text(text = "·", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textMuted)
}

@Composable
private fun StripAction(label: String, onClick: () -> Unit) {
    Text(
        text = label,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.accentSolid,
        modifier = Modifier
            .pressScale(onClick)
            .padding(vertical = UsTheme.spacing.xs)
            .semantics { role = Role.Button },
    )
}

/**
 * No ripple. The press is shown by the card dipping to 97% on a spring —
 * the feed card's gesture, quieter for a card this size.
 */
@Composable
internal fun Modifier.pressScale(onClick: () -> Unit): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "tubePress",
    )
    return this
        .clickable(interactionSource = interaction, indication = null, onClick = onClick)
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
}

private const val UNTITLED = "Untitled video"
private const val LANDSCAPE = 16f / 9f
private const val TITLE_LINES = 2
private const val BADGE_PLATE_ALPHA = 0.7f
private const val OVERLAY_ALPHA = 0.45f
private const val LOADER_TRACK_ALPHA = 0.25f
private const val PRESS_SCALE = 0.97f
private const val PRESS_STIFFNESS = 1200f
private val TITLE_SIZE = 16.sp
private val ROW_TITLE_SIZE = 14.sp
private val ROW_THUMB_WIDTH = 160.dp
private val LOADER_SIZE = 40.dp
private val LOADER_STROKE = 3.dp
