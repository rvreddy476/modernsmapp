package com.us.android.feature.tube.ui.home

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
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
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil3.compose.AsyncImage
import com.us.android.core.common.time.formatDuration
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.ContinueWatching
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.model.FeedItem
import com.us.android.feature.tube.ui.BlurHashImage
import com.us.android.feature.tube.ui.TubeRing
import com.us.android.feature.tube.ui.TubeViewer
import com.us.android.feature.tube.ui.pressScale

// ── Channels strip ──────────────────────────────────────────────────────

/**
 * The channels strip: circular avatars with the 2dp ember ring — the
 * viewer's own channel first, when they have one, then the creators they
 * follow, newest video first. A tap opens the channel's page inside Tube.
 * No "Create" bubble (founder, 2026-09-05): a viewer without a channel
 * simply has no bubble, and the strip is not drawn at all until there is
 * a followed channel to put in it.
 */
@Composable
internal fun ChannelsStrip(
    own: ChannelState,
    viewer: TubeViewer?,
    channels: List<TubeChannelBubble>,
    onOpenChannel: (userId: String) -> Unit,
    onOpenYou: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyRow(
        modifier = modifier
            .fillMaxWidth()
            .testTag("tube_channels"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        if (own is ChannelState.Present) {
            item(key = "you") {
                ChannelBubble(
                    name = "You",
                    avatarName = own.channel.name,
                    seed = own.channel.userId,
                    avatarUrl = own.channel.avatarUrl ?: viewer?.avatarUrl,
                    description = "Your channel, ${own.channel.name}",
                    onClick = onOpenYou,
                    modifier = Modifier.testTag("tube_channel:you"),
                )
            }
        }
        items(channels.size, key = { channels[it].userId }) { index ->
            val bubble = channels[index]
            ChannelBubble(
                name = bubble.name,
                avatarName = bubble.name,
                seed = bubble.userId,
                avatarUrl = bubble.avatarUrl,
                description = "Channel ${bubble.name}",
                onClick = { onOpenChannel(bubble.userId) },
                modifier = Modifier.testTag("tube_channel:${bubble.userId}"),
            )
        }
    }
}

@Composable
private fun ChannelBubble(
    name: String,
    avatarName: String,
    seed: String,
    avatarUrl: String?,
    description: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .width(BUBBLE_WIDTH)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        UsAvatar(name = avatarName, seed = seed, size = UsAvatarSize.Chat, imageUrl = avatarUrl, hasRing = true)
        BubbleLabel(name)
    }
}

@Composable
private fun BubbleLabel(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        fontSize = BUBBLE_LABEL_SIZE,
        color = UsTheme.extended.textSecondary,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        textAlign = TextAlign.Center,
    )
}

// ── Continue watching ───────────────────────────────────────────────────

/**
 * "Continue watching": a row of cards, each wearing an ember ring over
 * the still's bottom-left corner — the watched fraction as the sweep, the
 * time left inside it. Tapping one resumes it.
 */
@Composable
internal fun ContinueRow(
    rows: List<ContinueWatching>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.testTag("tube_continue")) {
        SectionTitle(title = "Continue watching")
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
    val remaining = (row.durationMs - row.positionMs).coerceAtLeast(0L)
    Column(
        modifier = modifier
            .width(CONTINUE_WIDTH)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Resume ${row.item.displayTitle}, ${formatDuration(remaining)} left"
            }
            .testTag("tube_continue:${row.item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(LANDSCAPE)
                .clip(RoundedCornerShape(UsTheme.radii.large))
                .background(UsTheme.extended.bgCard),
        ) {
            Still(thumb = thumb)
            // Over the still's bottom-left corner when there is a still; in
            // the middle of the raised card when nothing could be resolved.
            Box(
                modifier = Modifier
                    .align(if (thumb.url != null) Alignment.BottomStart else Alignment.Center)
                    .padding(CONTINUE_RING_INSET),
            ) {
                TubeRing(progress = row.fraction, modifier = Modifier.size(CONTINUE_RING)) {
                    Column(
                        modifier = Modifier
                            .fillMaxSize()
                            .clip(CircleShape)
                            .background(Color.Black.copy(alpha = RING_PLATE_ALPHA)),
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.Center,
                    ) {
                        Text(
                            text = formatDuration(remaining),
                            style = MaterialTheme.typography.labelMedium,
                            fontSize = RING_TIME_SIZE,
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                            maxLines = 1,
                        )
                        Text(
                            text = "left",
                            style = MaterialTheme.typography.labelSmall,
                            fontSize = RING_LEFT_SIZE,
                            color = Color.White.copy(alpha = META_ALPHA),
                            maxLines = 1,
                        )
                    }
                }
            }
        }
        TitleText(row.item.displayTitle, size = SMALL_TITLE_SIZE, maxLines = 1)
        Text(
            text = row.item.creatorName,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

// ── Reels panel ─────────────────────────────────────────────────────────

/**
 * The "Reels" panel: a glass panel with the film-reel glyph and the word,
 * then a row of 9:16 cards with the title on a bottom ramp — TWO to a
 * screen width (founder, 2026-09-05), each half the panel's inner width
 * less the gap, rounded 18dp; the row still scrolls sideways. A card opens
 * the app's Reels at that clip; a long-press opens its "more" sheet.
 */
@Composable
internal fun ReelsPanel(
    reels: List<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onMore: (FeedItem) -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(PANEL_RADIUS)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(vertical = UsTheme.spacing.l)
            .testTag("tube_reels"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            modifier = Modifier.padding(horizontal = UsTheme.spacing.xl),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Icon(
                imageVector = UsIcons.Reels,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier.size(PANEL_GLYPH),
            )
            Text(
                text = "Reels",
                style = MaterialTheme.typography.titleMedium,
                fontSize = SECTION_TITLE_SIZE,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
        }
        BoxWithConstraints(modifier = Modifier.fillMaxWidth()) {
            val cardWidth = reelCardWidth(panelWidth = maxWidth, inset = UsTheme.spacing.xl, gap = UsTheme.spacing.m)
            LazyRow(
                contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                items(reels.size, key = { reels[it].id }) { index ->
                    val reel = reels[index]
                    ReelCard(
                        item = reel,
                        thumb = thumbFor(reel),
                        width = cardWidth,
                        onClick = { onOpen(reel) },
                        onMore = { onMore(reel) },
                    )
                }
            }
        }
    }
}

/** Two cards across the panel's inner width: `(width − 2·inset − gap) / 2`. Pure, so the split is a test. */
internal fun reelCardWidth(panelWidth: Dp, inset: Dp, gap: Dp): Dp = (panelWidth - inset * 2 - gap) / 2

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ReelCard(item: FeedItem, thumb: VideoThumb, width: Dp, onClick: () -> Unit, onMore: () -> Unit) {
    Box(
        modifier = Modifier
            .width(width)
            .aspectRatio(PORTRAIT)
            .clip(RoundedCornerShape(REEL_RADIUS))
            .background(UsTheme.extended.bgCard)
            .combinedClickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
                onLongClick = onMore,
            )
            .semantics {
                role = Role.Button
                contentDescription = "Open reel ${item.displayTitle}"
            }
            .testTag("tube_reel:${item.id}"),
    ) {
        Still(thumb = thumb)
        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .fillMaxWidth()
                .height(REEL_SCRIM_HEIGHT)
                .background(BottomScrim),
        )
        Text(
            text = item.displayTitle,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
            maxLines = TITLE_LINES,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier
                .align(Alignment.BottomStart)
                .padding(UsTheme.spacing.m),
        )
    }
}

// ── The mosaic tile ─────────────────────────────────────────────────────

/** How many columns the mosaic has. */
internal const val GRID_COLUMNS = 2

/**
 * One tile of the two-column mosaic the Subscriptions, You and channel
 * pages keep (home lists full-width [VideoCard]s instead): the still with
 * the title INSIDE it on a bottom ramp, a tiny avatar and the channel's
 * name under the title, the length in a pill top-right, and a small white
 * ⋮ top-left. Landscape videos are 16:9, portrait ones 4:5, so the columns
 * stagger. The tile opens the video; ⋮ and a long-press open the "more"
 * sheet.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun GridCard(
    item: FeedItem,
    thumb: VideoThumb,
    onClick: () -> Unit,
    onMore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(if (thumb.isPortrait) PORTRAIT_TILE else LANDSCAPE)
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .combinedClickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
                onLongClick = onMore,
            )
            .semantics {
                role = Role.Button
                contentDescription = "Play ${item.displayTitle}"
            }
            .testTag("tube_card:${item.id}"),
    ) {
        Still(thumb = thumb)
        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .fillMaxWidth()
                .height(TILE_SCRIM_HEIGHT)
                .background(BottomScrim),
        )
        MoreGlyph(
            onClick = onMore,
            tint = Color.White,
            modifier = Modifier
                .align(Alignment.TopStart)
                .padding(UsTheme.spacing.xs)
                .testTag("tube_more:${item.id}"),
        )
        if (thumb.durationMs > 0L) {
            DurationBadge(
                text = formatDuration(thumb.durationMs),
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.m),
            )
        }
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .padding(UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            Text(
                text = item.displayTitle,
                style = MaterialTheme.typography.labelLarge,
                fontSize = TILE_TITLE_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = Color.White,
                maxLines = TITLE_LINES,
                overflow = TextOverflow.Ellipsis,
            )
            CreatorLine(item = item)
        }
    }
}

/** Tiny avatar · channel name — the credit line a tile carries on its ramp. */
@Composable
private fun CreatorLine(item: FeedItem) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        TinyAvatar(name = item.creatorName, url = item.channel?.avatarUrl)
        Text(
            text = item.creatorName,
            style = MaterialTheme.typography.labelSmall,
            fontSize = TINY_META_SIZE,
            color = Color.White.copy(alpha = META_ALPHA),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
    }
}

/**
 * A tile's credit avatar at a size the design system's avatars do not
 * come in: the photo when the channel has one, the initial on glass
 * otherwise. Decorative — the name is right beside it.
 */
@Composable
private fun TinyAvatar(name: String, url: String?) {
    Box(
        modifier = Modifier
            .size(TINY_AVATAR)
            .clip(CircleShape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        if (url.isNullOrBlank()) {
            Text(
                text = name.trim().take(1).uppercase(),
                style = MaterialTheme.typography.labelSmall,
                fontSize = TINY_INITIAL_SIZE,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
        } else {
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/** The wash, then the still over it as it loads. */
@Composable
internal fun Still(thumb: VideoThumb, modifier: Modifier = Modifier) {
    Box(modifier = modifier.fillMaxSize()) {
        BlurHashImage(hash = thumb.blurhash, modifier = Modifier.fillMaxSize())
        thumb.url?.let { url ->
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/** A section's title row in Outfit, with an optional white glyph. */
@Composable
internal fun SectionTitle(title: String, modifier: Modifier = Modifier) {
    Text(
        text = title,
        style = MaterialTheme.typography.titleMedium,
        fontSize = SECTION_TITLE_SIZE,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textPrimary,
        modifier = modifier
            .fillMaxWidth()
            .padding(start = UsTheme.spacing.pageHorizontal, end = UsTheme.spacing.s, top = UsTheme.spacing.l),
    )
}

/** Black at 70% on the bottom edge, gone at the top of the ramp. */
private val BottomScrim: Brush = Brush.verticalGradient(
    listOf(Color.Transparent, Color.Black.copy(alpha = 0.72f)),
)

private const val PORTRAIT = 9f / 16f
private const val PORTRAIT_TILE = 4f / 5f
private const val TITLE_LINES = 2
private const val META_ALPHA = 0.82f
private const val RING_PLATE_ALPHA = 0.45f
private val HAIRLINE = 1.dp
private val BUBBLE_WIDTH = 64.dp
private val BUBBLE_LABEL_SIZE = 11.sp
private val CONTINUE_WIDTH = 200.dp
private val CONTINUE_RING = 60.dp

/** The ring's distance from the still's corner (design: 12dp). */
private val CONTINUE_RING_INSET = 12.dp
private val RING_TIME_SIZE = 12.sp
private val RING_LEFT_SIZE = 9.sp
private val SMALL_TITLE_SIZE = 13.sp
private val PANEL_RADIUS = 20.dp
private val PANEL_GLYPH = 22.dp
private val SECTION_TITLE_SIZE = 18.sp
private val REEL_RADIUS = 18.dp
private val REEL_SCRIM_HEIGHT = 88.dp
private val TILE_SCRIM_HEIGHT = 96.dp
private val TILE_TITLE_SIZE = 13.sp
private val TINY_AVATAR = 16.dp
private val TINY_INITIAL_SIZE = 9.sp
private val TINY_META_SIZE = 11.sp
