package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.tube.data.ElementSurface
import com.us.android.feature.tube.data.ElementTarget
import com.us.android.feature.tube.data.EndScreenKind
import com.us.android.feature.tube.data.FRAME_ASPECT
import com.us.android.feature.tube.data.ViewerCard
import com.us.android.feature.tube.data.ViewerEndScreenElement
import com.us.android.feature.tube.data.WatchElements
import com.us.android.feature.tube.data.boxOf
import com.us.android.feature.tube.data.endScreensAllowed
import com.us.android.feature.tube.data.target
import com.us.android.feature.tube.data.teaserCardAt
import com.us.android.feature.tube.data.visibleEndScreenElements

/** The biggest 16:9 frame that fits [width] × [height], centred: its left, top, width and height. */
internal data class FittedFrame(val left: Dp, val top: Dp, val width: Dp, val height: Dp)

internal fun fitFrame(width: Dp, height: Dp): FittedFrame {
    if (width <= 0.dp || height <= 0.dp) return FittedFrame(0.dp, 0.dp, 0.dp, 0.dp)
    val w = minOf(width, height * FRAME_ASPECT.toFloat())
    val h = w / FRAME_ASPECT.toFloat()
    return FittedFrame((width - w) / 2, (height - h) / 2, w, h)
}

/**
 * What the creator placed over the video (web parity, 2026-10-24; the web's
 * VideoElementsOverlay): the end-screen elements in their windows — videos,
 * collections and links as 16:9 tiles, channels as circles — at the
 * fractions of the 16:9 frame they were placed at, and a card's teaser at
 * the top right for five seconds from its time. End screens need a frame at
 * least 480dp wide, as on the web, so a phone held upright shows only the
 * cards and fullscreen shows both. Each element counts one impression per
 * view; a tap counts a click and opens the target.
 */
@Composable
internal fun WatchElementsOverlay(
    elements: WatchElements?,
    playhead: Playhead,
    ended: Boolean,
    onShown: (ElementSurface, List<String>) -> Unit,
    onOpen: (ElementSurface, String, ElementTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (elements == null || (elements.endScreens.isEmpty() && elements.cards.isEmpty())) return
    BoxWithConstraints(modifier = modifier.fillMaxSize().testTag("watch_elements")) {
        val frame = fitFrame(maxWidth, maxHeight)
        val visible = if (endScreensAllowed(frame.width.value)) {
            visibleEndScreenElements(elements.endScreens, playhead.positionMs, ended, playhead.durationMs)
        } else {
            emptyList()
        }
        val teaser = if (ended) null else teaserCardAt(elements.cards, playhead.positionMs)
        val shownIds = visible.map { it.id }
        LaunchedEffect(shownIds) { if (shownIds.isNotEmpty()) onShown(ElementSurface.END_SCREENS, shownIds) }
        LaunchedEffect(teaser?.id) { teaser?.let { onShown(ElementSurface.CARDS, listOf(it.id)) } }

        Box(modifier = Modifier.offset(frame.left, frame.top).size(frame.width, frame.height)) {
            visible.forEach { element ->
                val box = boxOf(element.kind, element.position)
                val target = element.target() ?: return@forEach
                EndScreenTile(
                    element = element,
                    onClick = { onOpen(ElementSurface.END_SCREENS, element.id, target) },
                    modifier = Modifier
                        .offset(frame.width * box.x.toFloat(), frame.height * box.y.toFloat())
                        .size(frame.width * box.w.toFloat(), frame.height * box.h.toFloat()),
                )
            }
            if (teaser != null) {
                val target = teaser.target()
                if (target != null) {
                    CardTeaser(
                        card = teaser,
                        onClick = { onOpen(ElementSurface.CARDS, teaser.id, target) },
                        modifier = Modifier.align(Alignment.TopEnd).padding(top = TEASER_TOP, end = UsTheme.spacing.s),
                    )
                }
            }
        }
    }
}

@Composable
private fun EndScreenTile(element: ViewerEndScreenElement, onClick: () -> Unit, modifier: Modifier) {
    val circle = element.kind.isCircle
    val shape = if (circle) CircleShape else RoundedCornerShape(UsTheme.radii.small)
    val label = endScreenLabel(element)
    Box(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.stage, shape)
            .border(HAIRLINE, UsTheme.extended.onMedia.copy(alpha = OUTLINE_ALPHA), shape)
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { contentDescription = label }
            .testTag("watch_end_screen:${element.id}"),
    ) {
        val image = element.video?.thumbnailUrl ?: element.playlist?.thumbnailUrl ?: element.channel?.avatarUrl
        if (!image.isNullOrEmpty()) {
            AsyncImage(model = image, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        } else {
            Icon(
                imageVector = when (element.kind) {
                    EndScreenKind.EXTERNAL_LINK -> UsIcons.Link
                    EndScreenKind.PLAYLIST -> UsIcons.ListVideo
                    EndScreenKind.VIDEO -> UsIcons.Film
                    EndScreenKind.CHANNEL, EndScreenKind.CHANNEL_SUBSCRIBE -> UsIcons.Profile
                },
                contentDescription = null,
                tint = UsTheme.extended.onMedia,
                modifier = Modifier.align(Alignment.Center).size(GLYPH),
            )
        }
        if (!circle) {
            Text(
                text = element.video?.title ?: element.playlist?.title ?: element.link?.title.orEmpty(),
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .background(UsTheme.extended.stage.copy(alpha = CAPTION_ALPHA))
                    .padding(horizontal = UsTheme.spacing.xs, vertical = UsTheme.spacing.xs),
            )
        }
    }
}

/** What a screen reader hears for an element: its kind and its target's name. */
internal fun endScreenLabel(element: ViewerEndScreenElement): String = when (element.kind) {
    EndScreenKind.VIDEO -> "Video: ${element.video?.title.orEmpty()}"
    EndScreenKind.PLAYLIST -> "Collection: ${element.playlist?.title.orEmpty()}"
    EndScreenKind.CHANNEL_SUBSCRIBE -> "Subscribe: ${element.channel?.name.orEmpty()}"
    EndScreenKind.CHANNEL -> "Channel: ${element.channel?.name.orEmpty()}"
    EndScreenKind.EXTERNAL_LINK -> "Link: ${element.link?.domain.orEmpty()}"
}

/** A card's teaser: a small dark pill with the card's words and the info glyph. */
@Composable
private fun CardTeaser(card: ViewerCard, onClick: () -> Unit, modifier: Modifier) {
    Row(
        modifier = modifier
            .widthIn(max = TEASER_MAX)
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.stage.copy(alpha = TEASER_ALPHA))
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("watch_card:${card.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = UsIcons.Info, contentDescription = null, tint = UsTheme.extended.onMedia, modifier = Modifier.size(TEASER_GLYPH))
        Column {
            Text(
                text = card.teaser,
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

private val HAIRLINE = 1.dp
private val GLYPH = 20.dp
private val TEASER_TOP = 48.dp
private val TEASER_MAX = 220.dp
private val TEASER_GLYPH = 14.dp
private const val OUTLINE_ALPHA = 0.6f
private const val CAPTION_ALPHA = 0.55f
private const val TEASER_ALPHA = 0.7f
