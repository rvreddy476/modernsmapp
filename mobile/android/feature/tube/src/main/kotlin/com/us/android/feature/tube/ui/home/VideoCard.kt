package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.ui.BlurHashImage
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.formatDuration
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.videoMetaLine
import java.io.File

/** The title a card shows: the video's title, its caption, or the fallback. */
internal val FeedItem.displayTitle: String
    get() = title.ifBlank { text }.ifBlank { UNTITLED }

/**
 * One video as a full-width card (Tube redesign, 2026-09-05, from YouTube's
 * list): a 16:9 still with the length in its corner, then the author's
 * avatar beside the title — two lines, Outfit semibold — the quiet line of
 * author · views · age, and ⋮ at the right end. The card opens the video;
 * ⋮ opens the "more" sheet.
 */
@Composable
internal fun VideoCard(
    item: FeedItem,
    thumb: VideoThumb,
    onClick: () -> Unit,
    onMore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.m)
            .testTag("tube_card:${item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Thumbnail(
            thumb = thumb,
            coverPath = null,
            shape = RoundedCornerShape(UsTheme.radii.medium),
            modifier = Modifier
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .pressScale(onClick)
                .semantics {
                    role = Role.Button
                    contentDescription = "Play ${item.displayTitle}"
                },
        )
        CardMeta(item = item, onClick = onClick, onMore = onMore, titleSize = TITLE_SIZE)
    }
}

/**
 * The first video, large: the still runs edge to edge under a soft dark
 * ramp that lifts the length badge, and the description gets two lines of
 * its own under the title. Otherwise the same card.
 */
@Composable
internal fun HeroCard(
    item: FeedItem,
    thumb: VideoThumb,
    onClick: () -> Unit,
    onMore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.m)
            .testTag("tube_hero:${item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .pressScale(onClick)
                .semantics {
                    role = Role.Button
                    contentDescription = "Play ${item.displayTitle}"
                },
        ) {
            Thumbnail(thumb = thumb, coverPath = null, shape = RoundedCornerShape(0.dp), scrim = true)
        }
        CardMeta(
            item = item,
            onClick = onClick,
            onMore = onMore,
            titleSize = HERO_TITLE_SIZE,
            description = item.text.takeIf { it.isNotBlank() && it != item.displayTitle },
        )
    }
}

/** Avatar, title (+ description on the hero), the meta line, and ⋮ — the text half of a card. */
@Composable
private fun CardMeta(
    item: FeedItem,
    onClick: () -> Unit,
    onMore: () -> Unit,
    titleSize: androidx.compose.ui.unit.TextUnit,
    description: String? = null,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = UsTheme.spacing.pageHorizontal, end = UsTheme.spacing.s),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(name = item.author.nameForDisplay, seed = item.author.id, size = UsAvatarSize.Post)
        Column(
            modifier = Modifier
                .weight(1f)
                .pressScale(onClick),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            TitleText(item.displayTitle, size = titleSize)
            if (description != null) {
                Text(
                    text = description,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    maxLines = TITLE_LINES,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                text = videoMetaLine(item.author.nameForDisplay, item.createdAt, item.counts.views),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        MoreGlyph(onClick = onMore, modifier = Modifier.testTag("tube_more:${item.id}"))
    }
}

/** ⋮ — a 32dp target, no ripple. */
@Composable
internal fun MoreGlyph(onClick: () -> Unit, modifier: Modifier = Modifier, tint: Color = UsTheme.extended.textPrimary) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(MORE_TARGET)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "More"
            },
    ) {
        Icon(imageVector = UsIcons.More, contentDescription = null, tint = tint, modifier = Modifier.size(MORE_GLYPH))
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
            Thumbnail(
                thumb = VideoThumb(url = null, blurhash = "", durationMs = 0L),
                coverPath = head.coverPath,
                shape = RoundedCornerShape(UsTheme.radii.medium),
            )
            if (head.failure == null) {
                PublishingOverlay(modifier = Modifier.matchParentSize())
            }
        }
        head.failure?.let { failure ->
            PublishFailureStrip(message = failure.message, retryable = failure.retryable, onRetry, onDiscard)
        }
        TitleText(head.title.ifBlank { head.caption }.ifBlank { UNTITLED }, size = TITLE_SIZE)
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
    Row(
        modifier = modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Play ${item.displayTitle}"
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .testTag("tube_row:${item.id}"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Thumbnail(
            thumb = thumb,
            coverPath = null,
            shape = RoundedCornerShape(UsTheme.radii.medium),
            modifier = Modifier.width(ROW_THUMB_WIDTH),
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            TitleText(item.displayTitle, size = ROW_TITLE_SIZE)
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
internal fun TitleText(title: String, size: androidx.compose.ui.unit.TextUnit, maxLines: Int = TITLE_LINES) {
    Text(
        text = title,
        style = MaterialTheme.typography.titleMedium,
        fontSize = size,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.textPrimary,
        maxLines = maxLines,
        overflow = TextOverflow.Ellipsis,
    )
}

/**
 * The 16:9 frame: the BlurHash wash first, the still over it as it loads,
 * the length badge bottom-right. [coverPath] is a local file — the pending
 * card's cover — and wins over a URL when set. [scrim] draws the hero's
 * soft bottom ramp so the badge and the edge read over a bright frame.
 */
@Composable
internal fun Thumbnail(
    thumb: VideoThumb,
    coverPath: String?,
    shape: Shape,
    modifier: Modifier = Modifier,
    scrim: Boolean = false,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(LANDSCAPE)
            .clip(shape)
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
        if (scrim) {
            Box(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .fillMaxWidth()
                    .height(SCRIM_HEIGHT)
                    .background(BottomScrim),
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
internal fun DurationBadge(text: String, modifier: Modifier = Modifier) {
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

/** Black at 55% on the bottom edge, gone a third of the way up the frame. */
private val BottomScrim: Brush = Brush.verticalGradient(
    listOf(Color.Transparent, Color.Black.copy(alpha = 0.55f)),
)

private const val UNTITLED = "Untitled video"
internal const val LANDSCAPE = 16f / 9f
private const val TITLE_LINES = 2
private const val BADGE_PLATE_ALPHA = 0.7f
private const val OVERLAY_ALPHA = 0.45f
private const val LOADER_TRACK_ALPHA = 0.25f
private val TITLE_SIZE = 16.sp
private val HERO_TITLE_SIZE = 17.sp
private val ROW_TITLE_SIZE = 14.sp
private val ROW_THUMB_WIDTH = 160.dp
private val LOADER_SIZE = 40.dp
private val LOADER_STROKE = 3.dp
private val MORE_TARGET = 32.dp
private val MORE_GLYPH = 20.dp
private val SCRIM_HEIGHT: Dp = 72.dp
