package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
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
import com.us.android.core.feed.data.ContinueWatching
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.ui.BlurHashImage
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.formatDuration
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.videoMetaLine

/** A shelf's title row: an optional glyph in white (founder: icons stay white) and the title in Outfit. */
@Composable
internal fun ShelfHeader(
    title: String,
    modifier: Modifier = Modifier,
    icon: androidx.compose.ui.graphics.vector.ImageVector? = null,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(start = UsTheme.spacing.pageHorizontal, end = UsTheme.spacing.s, top = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        if (icon != null) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier.size(SHELF_GLYPH),
            )
        }
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontSize = SHELF_TITLE_SIZE,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
    }
}

/**
 * "Continue watching": a row of 160dp cards, each with a thin ember bar
 * under the still for how far the viewer got. Tapping one resumes it.
 */
@Composable
internal fun ContinueWatchingShelf(
    rows: List<ContinueWatching>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.testTag("tube_continue")) {
        ShelfHeader(title = "Continue watching")
        LazyRow(
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            items(rows.size, key = { rows[it].item.id }) { index ->
                val row = rows[index]
                ContinueCard(row = row, thumb = thumbFor(row.item), onClick = { onOpen(row.item) })
            }
        }
    }
}

@Composable
internal fun ContinueCard(
    row: ContinueWatching,
    thumb: VideoThumb,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .width(SHELF_CARD_WIDTH)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Resume ${row.item.displayTitle}"
            }
            .testTag("tube_continue:${row.item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Column(modifier = Modifier.clip(RoundedCornerShape(UsTheme.radii.small))) {
            Thumbnail(thumb = thumb, coverPath = null, shape = RoundedCornerShape(0.dp))
            ProgressBar(fraction = row.fraction)
        }
        TitleText(row.item.displayTitle, size = SHELF_TITLE_TEXT)
        Text(
            text = row.item.author.nameForDisplay,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** The ember bar under a continue-watching still: the watched part, on a dim track. */
@Composable
private fun ProgressBar(fraction: Float) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(PROGRESS_HEIGHT)
            .background(UsTheme.extended.textGhost)
            .semantics { contentDescription = "${(fraction * PERCENT).toInt()} percent watched" },
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(fraction.coerceAtLeast(MIN_PROGRESS))
                .height(PROGRESS_HEIGHT)
                .background(UsTheme.extended.ctaGradient),
        )
    }
}

/**
 * "Shorts": the reel glyph, the title, ⋮; then a row of 9:16 cards with
 * the reel's title over a bottom ramp and ⋮ in the corner. A card opens
 * the app's Reels at that reel; the shelf's ⋮ opens the first reel's sheet.
 */
@Composable
internal fun ShortsShelf(
    shorts: List<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.testTag("tube_shorts")) {
        ShelfHeader(title = "Shorts", icon = UsIcons.Reels)
        LazyRow(
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            items(shorts.size, key = { shorts[it].id }) { index ->
                val short = shorts[index]
                ShortCard(
                    item = short,
                    thumb = thumbFor(short),
                    onClick = { onOpen(short) },
                    onMore = { onMore(short) },
                )
            }
        }
    }
}

@Composable
private fun ShortCard(item: FeedItem, thumb: VideoThumb, onClick: () -> Unit, onMore: () -> Unit) {
    Box(
        modifier = Modifier
            .width(SHORT_WIDTH)
            .aspectRatio(PORTRAIT)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Open short ${item.displayTitle}"
            }
            .testTag("tube_short:${item.id}"),
    ) {
        BlurHashImage(hash = thumb.blurhash, modifier = Modifier.fillMaxSize())
        thumb.url?.let { url ->
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .fillMaxWidth()
                .height(SHORT_SCRIM_HEIGHT)
                .background(ShortScrim),
        )
        MoreGlyph(onClick = onMore, tint = Color.White, modifier = Modifier.align(Alignment.TopEnd))
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .padding(UsTheme.spacing.m),
        ) {
            Text(
                text = item.displayTitle,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = Color.White,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (thumb.durationMs > 0L) {
                Text(
                    text = formatDuration(thumb.durationMs),
                    style = MaterialTheme.typography.labelSmall,
                    color = Color.White.copy(alpha = SHORT_META_ALPHA),
                )
            }
        }
    }
}

private val ShortScrim: Brush = Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.75f)))

private const val PORTRAIT = 9f / 16f
private const val PERCENT = 100f
private const val MIN_PROGRESS = 0.02f
private const val SHORT_META_ALPHA = 0.8f
private val SHELF_GLYPH = 22.dp
private val SHELF_TITLE_SIZE = 18.sp
private val SHELF_TITLE_TEXT = 13.sp
internal val SHELF_CARD_WIDTH = 160.dp
private val SHORT_WIDTH = 132.dp
private val SHORT_SCRIM_HEIGHT = 96.dp
private val PROGRESS_HEIGHT = 3.dp

/**
 * A 160dp card for the You page's shelves — the still, two lines of title,
 * the views · age line, and ⋮ under the corner. The same width as a
 * continue-watching card so the page's rows line up.
 */
@Composable
internal fun ShelfVideoCard(
    item: FeedItem,
    thumb: VideoThumb,
    onClick: () -> Unit,
    onMore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .width(SHELF_CARD_WIDTH)
            .testTag("tube_shelf_card:${item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Thumbnail(
            thumb = thumb,
            coverPath = null,
            shape = RoundedCornerShape(UsTheme.radii.small),
            modifier = Modifier
                .pressScale(onClick)
                .semantics {
                    role = Role.Button
                    contentDescription = "Play ${item.displayTitle}"
                },
        )
        Row(verticalAlignment = Alignment.Top) {
            Column(
                modifier = Modifier
                    .weight(1f)
                    .pressScale(onClick),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                TitleText(item.displayTitle, size = SHELF_TITLE_TEXT)
                Text(
                    text = videoMetaLine(null, item.createdAt, item.counts.views),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            MoreGlyph(onClick = onMore)
        }
    }
}
