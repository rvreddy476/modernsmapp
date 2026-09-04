package com.us.android.feature.tube.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
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
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.ui.UnreadBadgeViewModel
import com.us.android.feature.tube.ui.home.TubeChip

/**
 * A Tube page's frame: Tube's header on top, Tube's bar underneath, the
 * page between (Tube redesign, 2026-09-05). The shell's bar is already
 * gone — every Tube route is a pushed screen, not a tab root — so the bar
 * here is the only one on screen.
 */
@Composable
fun TubePage(
    selected: TubeTab,
    onOpenNotifications: () -> Unit,
    onOpenSearch: () -> Unit,
    onBarAction: (TubeBarAction) -> Unit,
    content: @Composable (PaddingValues) -> Unit,
) {
    UsScaffold(
        applyPageGutter = false,
        topBar = { TubeHeader(onOpenNotifications = onOpenNotifications, onOpenSearch = onOpenSearch) },
        bottomBar = { TubeBottomBar(selected = selected, onAction = onBarAction) },
        content = content,
    )
}

/**
 * Tube's header: the wordmark — a small ember play badge and "Tube" in
 * Outfit — on the left; the bell with its unread count and search on the
 * right. No back arrow: Tube is a mini-app with its own bar, and the
 * system Back is the way out.
 */
@Composable
fun TubeHeader(
    onOpenNotifications: () -> Unit,
    onOpenSearch: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: UnreadBadgeViewModel = hiltViewModel(),
) {
    val unread by viewModel.count.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { viewModel.refresh() }

    Row(
        modifier = modifier
            .fillMaxWidth()
            .statusBarsPadding()
            .height(HEADER_HEIGHT)
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("tube_header"),
        verticalAlignment = Alignment.CenterVertically,
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
        HeaderGlyph(onClick = onOpenSearch, description = "Search videos", modifier = Modifier.testTag("tube_search")) {
            Icon(imageVector = UsIcons.Search, contentDescription = null, tint = UsTheme.extended.textPrimary)
        }
    }
}

/** The ember play tile and the name — one heading for a screen reader. */
@Composable
private fun TubeWordmark(modifier: Modifier = Modifier) {
    Row(
        modifier = modifier.semantics(mergeDescendants = true) { heading() },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(BADGE_SIZE)
                .background(UsTheme.extended.ctaGradient, RoundedCornerShape(BADGE_RADIUS)),
        ) {
            Icon(
                imageVector = UsIcons.Play,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(BADGE_GLYPH),
            )
        }
        Text(
            text = "Tube",
            style = MaterialTheme.typography.titleLarge,
            fontSize = WORDMARK_SIZE,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/** A 40dp target with no ripple; the glyph dips on press like every other Tube control. */
@Composable
private fun HeaderGlyph(
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
 * The rail under the header: the compass square that opens the app's
 * launcher, then the pills — the selected one WHITE with navy text (the
 * bar's rule: selected is white, never the accent), the rest on the raised
 * surface behind a hairline. Sticks under the header as the list scrolls.
 */
@Composable
fun TubeChipRail(
    chips: List<TubeChip>,
    selected: TubeChip,
    onSelect: (TubeChip) -> Unit,
    onOpenExplore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCanvas)
            .padding(vertical = UsTheme.spacing.m)
            .testTag("tube_chips"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        CompassSquare(
            onClick = onOpenExplore,
            modifier = Modifier.padding(start = UsTheme.spacing.pageHorizontal, end = UsTheme.spacing.s),
        )
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            contentPadding = PaddingValues(start = UsTheme.spacing.s, end = UsTheme.spacing.pageHorizontal),
        ) {
            items(chips.size, key = { chips[it].key }) { index ->
                val chip = chips[index]
                TubeChipPill(chip = chip, active = chip == selected, onClick = { onSelect(chip) })
            }
        }
    }
}

@Composable
private fun CompassSquare(onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(CHIP_HEIGHT)
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderMedium, shape)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Explore apps"
            }
            .testTag("tube_explore"),
    ) {
        Icon(
            imageVector = UsIcons.Compass,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(CHIP_GLYPH),
        )
    }
}

@Composable
private fun TubeChipPill(chip: TubeChip, active: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val fill = if (active) Color.White else UsTheme.extended.bgRaised
    val outline = if (active) Color.White else UsTheme.extended.borderMedium
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
private val BADGE_SIZE = 22.dp
private val BADGE_RADIUS = 6.dp
private val BADGE_GLYPH = 12.dp
private val WORDMARK_SIZE = 22.sp
private val GLYPH_TARGET = 40.dp
private val CHIP_HEIGHT = 32.dp
private val CHIP_GLYPH = 18.dp
private val CHIP_HORIZONTAL = 14.dp
private val CHIP_TEXT = 13.sp
private val HAIRLINE = 1.dp
