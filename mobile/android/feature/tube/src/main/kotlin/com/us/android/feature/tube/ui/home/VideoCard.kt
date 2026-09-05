package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.background
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
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
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
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.ui.BlurHashImage
import com.us.android.feature.tube.ui.formatDuration
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.videoMetaLine

/** The title a card shows: the video's title, its caption, or the fallback. */
internal val FeedItem.displayTitle: String
    get() = title.ifBlank { text }.ifBlank { UNTITLED }

/**
 * The full-width card Tube home lists its videos in (founder, 2026-09-05:
 * "long videos, full width of the screen, like YouTube"): the 16:9 still
 * across the page with 16dp corners and the length in a pill bottom-right,
 * then the channel's avatar beside the title (two lines) and the
 * "channel · views · time" line, with ⋮ at the end. The card opens the
 * video; ⋮ opens its "more" sheet. A portrait video sits letterboxed in
 * the same 16:9 frame, so every card is the same shape.
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
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Play ${item.displayTitle}"
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("tube_card:${item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Thumbnail(thumb = thumb, shape = RoundedCornerShape(UsTheme.radii.large))
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalAlignment = Alignment.Top,
        ) {
            UsAvatar(
                name = item.creatorName,
                seed = item.channel?.userId ?: item.authorId,
                size = UsAvatarSize.Post,
                imageUrl = item.channel?.avatarUrl,
            )
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                TitleText(item.displayTitle, size = CARD_TITLE_SIZE)
                MetaLine(item)
            }
            MoreGlyph(
                onClick = onMore,
                tint = UsTheme.extended.textSecondary,
                modifier = Modifier.testTag("tube_more:${item.id}"),
            )
        }
    }
}

/** The "more" glyph — a 32dp target, no ripple. */
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
            shape = RoundedCornerShape(UsTheme.radii.medium),
            modifier = Modifier.width(ROW_THUMB_WIDTH),
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            TitleText(item.displayTitle, size = ROW_TITLE_SIZE)
            MetaLine(item)
        }
    }
}

/** "channel · views · time", one line, muted. */
@Composable
private fun MetaLine(item: FeedItem) {
    Text(
        text = videoMetaLine(item.creatorName, item.createdAt, item.counts.views),
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
    )
}

@Composable
internal fun TitleText(title: String, size: TextUnit, maxLines: Int = TITLE_LINES) {
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
 * the length badge bottom-right. A landscape still fills the frame; a
 * portrait one is fitted whole in the middle, letterboxed on the raised
 * ground with its own wash showing either side.
 */
@Composable
internal fun Thumbnail(
    thumb: VideoThumb,
    shape: Shape,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(LANDSCAPE)
            .clip(shape)
            .background(if (thumb.isPortrait) UsTheme.extended.bgRaised else UsTheme.extended.bgCard),
    ) {
        BlurHashImage(hash = thumb.blurhash, modifier = Modifier.fillMaxSize())
        thumb.url?.let { url ->
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = if (thumb.isPortrait) ContentScale.Fit else ContentScale.Crop,
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

/** `m:ss` on a dark pill — the corner badge. */
@Composable
internal fun DurationBadge(text: String, modifier: Modifier = Modifier) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.SemiBold,
        color = Color.White,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(Color.Black.copy(alpha = BADGE_PLATE_ALPHA))
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .semantics { contentDescription = "Length $text" }
            .testTag("tube_duration"),
    )
}

private const val UNTITLED = "Untitled video"
internal const val LANDSCAPE = 16f / 9f
private const val TITLE_LINES = 2
private const val BADGE_PLATE_ALPHA = 0.7f
private val CARD_TITLE_SIZE = 15.sp
private val ROW_TITLE_SIZE = 14.sp
private val ROW_THUMB_WIDTH = 160.dp
private val MORE_TARGET = 32.dp
private val MORE_GLYPH = 20.dp
