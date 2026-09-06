package com.us.android.feature.tube.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.icon.VIDEO_MARK_BODY
import com.us.android.core.designsystem.icon.VIDEO_MARK_PLAY
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.ui.channel.CreateChannelSheet
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.home.TubeChip

/**
 * A Tube page's frame (Momentum look, 2026-09-05): the header on top,
 * Tube's flat bar stuck to the bottom edge, the page between — the app's
 * own scaffold, the way every other screen is framed, so the bar is
 * anchored exactly as the shell's is. The content is handed the bar's
 * measured height as bottom padding, so a list's last card clears it while
 * the list itself may scroll beneath. The shell's bar is already gone:
 * every Tube route is a pushed screen, not a tab root, so this bar is the
 * only one on screen.
 *
 * The header's More (≡) opens [TubeMenuSheet] over the page — the frame
 * owns that state, so every page gets the same sheet from the same glyph
 * and none has to mount it — and a "Create your channel" row from it
 * opens the create sheet once the menu has left.
 *
 * [selected] is null on a page the bar does not own (a channel page, the
 * scheduled list, saved videos); [onBack] puts the back glyph before the
 * mark on a page pushed inside Tube.
 */
@Composable
fun TubePage(
    selected: TubeTab?,
    destinations: TubeDestinations,
    onBack: (() -> Unit)? = null,
    content: @Composable (PaddingValues) -> Unit,
) {
    var menuOpen by rememberSaveable { mutableStateOf(false) }
    var createOpen by rememberSaveable { mutableStateOf(false) }
    UsScaffold(
        applyPageGutter = false,
        topBar = {
            TubeHeader(
                onOpenMenu = { menuOpen = true },
                onOpenSearch = destinations.onOpenSearch,
                onCreate = destinations.onCreateVideo,
                onBack = onBack,
            )
        },
        bottomBar = { TubeBottomBar(selected = selected, onAction = destinations::onBarAction) },
    ) { inner ->
        // The header's height is taken here, once; the pages only ever ask
        // the padding for its bottom, which is the bar.
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(top = inner.calculateTopPadding()),
        ) {
            content(PaddingValues(bottom = inner.calculateBottomPadding()))
        }
    }
    if (menuOpen) {
        TubeMenuSheet(
            destinations = destinations,
            onCreateChannel = { createOpen = true },
            onDismiss = { menuOpen = false },
        )
    }
    if (createOpen) {
        CreateChannelSheet(
            onCreated = {
                createOpen = false
                destinations.onOpenTab(TubeTab.YOU)
            },
            onDismiss = { createOpen = false },
        )
    }
}

/**
 * Tube's header (founder, 2026-09-05): the video mark — Momentum's
 * camera-and-play glyph on a raised tile, no name yet ("remove the name,
 * we think of a better one later") — on the left, and at the right corner
 * the create "+", the More hamburger and Search, in the same Material bar
 * so the glyphs sit at the same size and spacing. No bell and no avatar
 * (You is on the bar), and no search pill under it: the glyph is the one
 * way into search. [onBack] adds the back glyph before the mark on a page
 * pushed inside Tube; Tube's own roots have none, the system Back is the
 * way out.
 *
 * ## WHY THE "+" IS UP HERE (founder, 2026-09-06)
 *
 * "When in Tube, at the top give a plus button to create new videos."
 * Tube's create affordance existed already — but only as the unlabelled
 * raised tile in the MIDDLE OF THE BOTTOM BAR, identical to the shell's,
 * which reads as the app's create rather than "post a video here", and is
 * the last place a reader looks for it. So the same action is offered at
 * the top, on a raised tile of its own so it reads as a button rather than
 * a third grey glyph. The bar's tile stays: both open the same sheet.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TubeHeader(
    onOpenMenu: () -> Unit,
    onOpenSearch: () -> Unit,
    onCreate: () -> Unit,
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
) {
    TopAppBar(
        title = { TubeWordmark() },
        navigationIcon = {
            if (onBack != null) {
                HeaderAction(icon = UsIcons.Back, description = "Back", onClick = onBack, tag = "tube_back")
            }
        },
        actions = {
            HeaderCreate(onClick = onCreate)
            HeaderAction(icon = UsIcons.Menu, description = "More", onClick = onOpenMenu, tag = "tube_menu")
            HeaderAction(icon = UsIcons.Search, description = "Search", onClick = onOpenSearch, tag = "tube_search")
        },
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
        modifier = modifier.testTag("tube_header"),
    )
}

/**
 * The header's "+": the plus on a raised tile with a hairline glass edge —
 * the wordmark badge's idiom, so it reads as a control and not as another
 * bare glyph beside More and Search. Same 48dp target as its neighbours.
 */
@Composable
private fun HeaderCreate(onClick: () -> Unit) {
    val shape = RoundedCornerShape(CREATE_RADIUS)
    HeaderGlyph(
        onClick = onClick,
        description = "Create video",
        size = ACTION_TARGET,
        modifier = Modifier.testTag("tube_create"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(CREATE_TILE)
                .background(UsTheme.extended.bgRaised, shape)
                .border(BADGE_HAIRLINE, UsTheme.extended.glassBorder, shape),
        ) {
            Icon(
                imageVector = UsIcons.Create,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(CREATE_GLYPH),
            )
        }
    }
}

/** One of the header's glyphs: a 48dp target, the icon in white, no ripple — a dip on press. */
@Composable
private fun HeaderAction(icon: ImageVector, description: String, onClick: () -> Unit, tag: String) {
    HeaderGlyph(
        onClick = onClick,
        description = description,
        size = ACTION_TARGET,
        modifier = Modifier.testTag(tag),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = Color.White)
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

/** A square target with no ripple; the glyph dips on press like every other Tube control. */
@Composable
internal fun HeaderGlyph(
    onClick: () -> Unit,
    description: String,
    modifier: Modifier = Modifier,
    size: Dp = GLYPH_TARGET,
    content: @Composable () -> Unit,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(size)
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

private const val MARK_VIEWPORT = 24f
private const val MARK_STROKE = 1.9f
private val BADGE_SIZE = 36.dp
private val BADGE_RADIUS = 11.dp
private val BADGE_HAIRLINE = 1.dp
private val BADGE_GLYPH = 24.dp
private val CREATE_TILE = 32.dp
private val CREATE_RADIUS = 10.dp
private val CREATE_GLYPH = 20.dp
private val GLYPH_TARGET = 40.dp

/** The Reels header's target: Material's icon button, 48dp around a 24dp glyph. */
private val ACTION_TARGET = 48.dp
private val CHIP_HEIGHT = 34.dp
private val CHIP_HORIZONTAL = 14.dp
private val CHIP_TEXT = 13.sp
private val HAIRLINE = 1.dp
