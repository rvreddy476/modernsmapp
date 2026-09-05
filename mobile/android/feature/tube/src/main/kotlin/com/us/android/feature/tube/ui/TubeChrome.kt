package com.us.android.feature.tube.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.icon.VIDEO_MARK_BODY
import com.us.android.core.designsystem.icon.VIDEO_MARK_PLAY
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.ui.UnreadBadgeViewModel
import com.us.android.feature.tube.ui.home.TubeChip

/**
 * A Tube page's frame (Momentum look, 2026-09-05): the wordmark header and
 * the glass search pill on top, the floating bar over the bottom, the page
 * between. The bar FLOATS — the page scrolls under it — so the content is
 * handed the clearance it needs as bottom padding rather than a reserved
 * strip. The shell's bar is already gone: every Tube route is a pushed
 * screen, not a tab root, so this bar is the only one on screen.
 *
 * [selected] is null on a page the bar does not own (a channel page).
 */
@Composable
fun TubePage(
    selected: TubeTab?,
    onOpenNotifications: () -> Unit,
    onOpenSearch: () -> Unit,
    onOpenYou: () -> Unit,
    onBarAction: (TubeBarAction) -> Unit,
    topBar: @Composable () -> Unit = {
        TubeHeader(onOpenNotifications = onOpenNotifications, onOpenYou = onOpenYou)
        TubeSearchPill(onClick = onOpenSearch)
    },
    content: @Composable (PaddingValues) -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background),
    ) {
        topBar()
        Box(modifier = Modifier.weight(1f)) {
            content(PaddingValues(bottom = TubeBarClearance))
            TubeBottomBar(
                selected = selected,
                onAction = onBarAction,
                modifier = Modifier.align(Alignment.BottomCenter),
            )
        }
    }
}

/**
 * Tube's header: the video mark — Momentum's video-and-play glyph on an
 * ember tile, no name yet (founder, 2026-09-05: "remove the name, we think
 * of a better one later") — on the left; the bell with its unread count and the viewer's
 * own avatar (which opens You) on the right. No back arrow: Tube is a
 * mini-app with its own bar, and the system Back is the way out.
 */
@Composable
fun TubeHeader(
    onOpenNotifications: () -> Unit,
    onOpenYou: () -> Unit,
    modifier: Modifier = Modifier,
    badge: UnreadBadgeViewModel = hiltViewModel(),
    viewer: TubeViewerViewModel = hiltViewModel(),
) {
    val unread by badge.count.collectAsStateWithLifecycle()
    val who by viewer.viewer.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { badge.refresh() }

    Row(
        modifier = modifier
            .fillMaxWidth()
            .statusBarsPadding()
            .height(HEADER_HEIGHT)
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("tube_header"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        TubeWordmark(modifier = Modifier.weight(1f))
        HeaderGlyph(
            onClick = onOpenNotifications,
            description = when {
                unread <= 0 -> "Notifications"
                unread == 1 -> "Notifications, 1 unread"
                else -> "Notifications, $unread unread"
            },
            modifier = Modifier.testTag("tube_bell"),
        ) {
            UsBadgedIcon(icon = UsIcons.Notifications, count = unread)
        }
        HeaderGlyph(onClick = onOpenYou, description = "You", modifier = Modifier.testTag("tube_avatar")) {
            UsAvatar(
                name = who?.name ?: "You",
                seed = who?.userId ?: "you",
                size = UsAvatarSize.Small,
                imageUrl = who?.avatarUrl,
            )
        }
    }
}

/**
 * The video mark, alone, in the theme (founder, 2026-09-05: "not orange —
 * our theme; colourful is fine, white is fine"): a raised navy tile with a
 * hairline glass border, the camcorder outline in white, and the play
 * triangle inside it in a violet-to-cyan sweep from the launcher palette.
 * It is the page's heading for a screen reader, which hears "Videos"
 * until the product has its name.
 */
@Composable
internal fun TubeWordmark(modifier: Modifier = Modifier) {
    Row(
        modifier = modifier.semantics(mergeDescendants = true) {
            heading()
            contentDescription = "Videos"
        },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val shape = RoundedCornerShape(BADGE_RADIUS)
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(BADGE_SIZE)
                .background(UsTheme.extended.bgRaised, shape)
                .border(BADGE_HAIRLINE, UsTheme.extended.glassBorder, shape),
        ) {
            VideoMark(modifier = Modifier.size(BADGE_GLYPH))
        }
    }
}

/**
 * The mark itself, painted: the camcorder body and lens flap stroked in
 * white at Lucide's weight, the play triangle filled with a sweep from the
 * Chat violet to the Shop cyan — two of the app's own colours, so the mark
 * is colourful without borrowing anyone's red. Same paths as
 * [UsIcons.VideoPlay], scaled from the 24-unit viewport.
 */
@Composable
private fun VideoMark(modifier: Modifier = Modifier) {
    val launcher = UsTheme.extended.launcher
    val sweep = Brush.linearGradient(listOf(launcher.chat.glow, launcher.shop.glow))
    val body = remember { PathParser().parsePathString(VIDEO_MARK_BODY).toPath() }
    val play = remember { PathParser().parsePathString(VIDEO_MARK_PLAY).toPath() }
    Canvas(modifier = modifier) {
        val s = size.minDimension / MARK_VIEWPORT
        scale(scaleX = s, scaleY = s, pivot = Offset.Zero) {
            drawPath(
                path = body,
                color = Color.White,
                style = Stroke(width = MARK_STROKE, cap = StrokeCap.Round, join = StrokeJoin.Round),
            )
            drawPath(path = play, brush = sweep)
        }
    }
}

/** A 40dp target with no ripple; the glyph dips on press like every other Tube control. */
@Composable
internal fun HeaderGlyph(
    onClick: () -> Unit,
    description: String,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(GLYPH_TARGET)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            },
    ) {
        content()
    }
}

/**
 * The search entry under the header: a full-width glass pill with the
 * search glyph and "Search videos, channels". The entry itself, not an
 * icon (founder, 2026-09-05) — it opens the app's Explore scoped to videos.
 */
@Composable
fun TubeSearchPill(onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .height(SEARCH_HEIGHT)
            .background(UsTheme.extended.glassBg, shape)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Search videos, channels"
            }
            .padding(horizontal = UsTheme.spacing.xxl)
            .testTag("tube_search"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(
            imageVector = UsIcons.Search,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(SEARCH_GLYPH),
        )
        Text(
            text = "Search videos, channels",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            maxLines = 1,
        )
    }
}

/**
 * The chip rail: glass pills, scrolling — "All", "Following", then the
 * categories. The selected one is WHITE with navy text (the bar's rule:
 * selected is white, never the accent). The compass square is gone:
 * Explore is one tap away on the shell's bar.
 */
@Composable
fun TubeChipRail(
    chips: List<TubeChip>,
    selected: TubeChip,
    onSelect: (TubeChip) -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyRow(
        modifier = modifier
            .fillMaxWidth()
            .testTag("tube_chips"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
    ) {
        items(chips.size, key = { chips[it].key }) { index ->
            val chip = chips[index]
            TubeChipPill(chip = chip, active = chip == selected, onClick = { onSelect(chip) })
        }
    }
}

@Composable
private fun TubeChipPill(chip: TubeChip, active: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val fill = if (active) Color.White else UsTheme.extended.glassBg
    val outline = if (active) Color.White else UsTheme.extended.glassBorder
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .height(CHIP_HEIGHT)
            .background(fill, shape)
            .border(HAIRLINE, outline, shape)
            .pressScale(onClick)
            .semantics {
                role = Role.Tab
                selected = active
            }
            .padding(horizontal = CHIP_HORIZONTAL)
            .testTag("tube_chip:${chip.key}"),
    ) {
        Text(
            text = chip.label,
            style = MaterialTheme.typography.labelLarge,
            fontSize = CHIP_TEXT,
            fontWeight = FontWeight.SemiBold,
            color = if (active) UsTheme.extended.brandNavy else UsTheme.extended.textPrimary,
            maxLines = 1,
        )
    }
}

private val HEADER_HEIGHT = 56.dp
private const val MARK_VIEWPORT = 24f
private const val MARK_STROKE = 1.9f
private val BADGE_SIZE = 36.dp
private val BADGE_RADIUS = 11.dp
private val BADGE_HAIRLINE = 1.dp
private val BADGE_GLYPH = 24.dp
private val GLYPH_TARGET = 40.dp
private val SEARCH_HEIGHT = 44.dp
private val SEARCH_GLYPH = 18.dp
private val CHIP_HEIGHT = 34.dp
private val CHIP_HORIZONTAL = 14.dp
private val CHIP_TEXT = 13.sp
private val HAIRLINE = 1.dp
