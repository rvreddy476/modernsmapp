package com.us.android.feature.post.createhub

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.launch

/**
 * The Create sheet — what the bar's "+" opens, over whatever is on screen.
 *
 * ## CHOOSE ONCE, THEN COMPOSE
 *
 * Six typed tiles (founder render, 2026-09-04) and a Go Live row. Picking a
 * tile closes the sheet and pushes the composer for exactly that type; there
 * is no rail inside the composer to switch afterwards. The old create hub put
 * the choice at the BOTTOM of a full page the user had already navigated to;
 * a sheet puts it on top of where they are, so backing out costs nothing.
 *
 * ## WHAT IS NOT HERE
 *
 * No "Drafts" link. The server keeps `post_drafts`, but this app has no drafts
 * screen or route to open — the composer restores its ONE durable draft on
 * its own (`ComposerDraftStore`). A link to a list that does not exist would
 * be dead UI, so it is omitted rather than stubbed.
 *
 * [onPick] receives the chosen surface; `:app` turns it into the route. The
 * sheet never navigates itself — the feature does not own the graph.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CreateSheet(
    onPick: (CreateSurface) -> Unit,
    onOpenLive: () -> Unit,
    onDismiss: () -> Unit,
    viewModel: CreateSheetViewModel = hiltViewModel(),
) {
    val compact by viewModel.compact.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()

    // Slide the sheet away FIRST, then act: navigating while the sheet is
    // still up would leave the composer arriving under a scrim.
    fun leaveThen(action: () -> Unit) {
        scope.launch { sheetState.hide() }.invokeOnCompletion {
            onDismiss()
            action()
        }
    }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        // The handle is drawn inside the content so it sits under the 1dp top
        // edge rather than above it.
        dragHandle = null,
        modifier = Modifier.testTag("create-sheet"),
    ) {
        val edge = UsTheme.extended.borderMedium
        val edgeWidth = with(LocalDensity.current) { EDGE.toPx() }
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .drawBehind {
                    drawLine(
                        color = edge,
                        start = Offset(0f, edgeWidth / 2),
                        end = Offset(size.width, edgeWidth / 2),
                        strokeWidth = edgeWidth,
                    )
                }
                .padding(horizontal = CONTENT_PADDING)
                .padding(bottom = CONTENT_PADDING)
                .navigationBarsPadding(),
        ) {
            GrabHandle()
            SheetHeader(compact = compact, onCompactChanged = viewModel::setCompact)
            Spacer(Modifier.height(HEADER_GAP))

            if (compact) {
                CompactList(onPick = { leaveThen { onPick(it) } })
            } else {
                TileGrid(onPick = { leaveThen { onPick(it) } })
            }

            Spacer(Modifier.height(GAP))
            GoLiveRow(onClick = { leaveThen(onOpenLive) })
        }
    }
}

// ── Header ──────────────────────────────────────────────────────────────

/** 36×4, muted at 40%: a handle, not a decoration. */
@Composable
private fun GrabHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = HANDLE_TOP, bottom = HANDLE_BOTTOM),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
        )
    }
}

/**
 * "Create" and the Compact view pill. The pill is a toggle, and says so to a
 * screen reader through its selected state rather than a second label.
 */
@Composable
private fun SheetHeader(compact: Boolean, onCompactChanged: (Boolean) -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = "Create",
            style = MaterialTheme.typography.headlineMedium.copy(
                fontWeight = FontWeight.ExtraBold,
                fontSize = TITLE_SIZE,
            ),
            color = UsTheme.extended.textPrimary,
        )
        val pillShape = RoundedCornerShape(PILL_RADIUS)
        Box(
            modifier = Modifier
                .clip(pillShape)
                .border(EDGE, UsTheme.extended.borderMedium, pillShape)
                .background(
                    if (compact) UsTheme.extended.glassBg else Color.Transparent,
                )
                .clickable { onCompactChanged(!compact) }
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                .semantics {
                    contentDescription = "Compact view"
                    role = Role.Switch
                }
                .testTag("create-compact-toggle"),
        ) {
            Text(
                text = "Compact view",
                style = MaterialTheme.typography.bodyLarge.copy(fontSize = PILL_TEXT_SIZE),
                color = UsTheme.extended.textPrimary,
            )
        }
    }
}

// ── The grid ────────────────────────────────────────────────────────────

/** 3 columns × 2 rows, 12dp gaps. Rows, not a lazy grid: six items, all visible. */
@Composable
private fun TileGrid(onPick: (CreateSurface) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(GAP)) {
        CreateSurface.entries.chunked(COLUMNS).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(GAP)) {
                row.forEach { surface ->
                    CreateTile(
                        surface = surface,
                        onClick = { onPick(surface) },
                        modifier = Modifier.weight(1f),
                    )
                }
            }
        }
    }
}

/**
 * One tile: gradient circle, white glyph, label, hint. Pressed: a 2dp lift
 * and a 20% darker fill, so the tap reads on a surface with no ripple colour
 * of its own.
 */
@Composable
private fun CreateTile(surface: CreateSurface, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val shape = RoundedCornerShape(TILE_RADIUS)
    val lift = with(LocalDensity.current) { PRESS_LIFT.toPx() }

    Column(
        modifier = modifier
            .graphicsLayer { translationY = if (pressed) -lift else 0f }
            .height(TILE_HEIGHT)
            .clip(shape)
            .background(UsTheme.extended.bgRaised)
            .border(EDGE, UsTheme.extended.borderMedium, shape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .background(if (pressed) Color.Black.copy(alpha = PRESS_DARKEN) else Color.Transparent)
            .semantics {
                contentDescription = "Create ${surface.label.lowercase()}. ${surface.hint}"
                role = Role.Button
            }
            .testTag("create-tile-${surface.routeKey}"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        GlyphCircle(brush = surface.brush(), icon = surface.icon, size = TILE_CIRCLE, glyph = TILE_GLYPH)
        Spacer(Modifier.height(UsTheme.spacing.l))
        Text(
            text = surface.label,
            style = MaterialTheme.typography.bodyLarge.copy(
                fontWeight = FontWeight.Bold,
                fontSize = LABEL_SIZE,
            ),
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = surface.hint,
            style = MaterialTheme.typography.bodyLarge.copy(fontSize = HINT_SIZE),
            color = UsTheme.extended.textMuted,
            // One line, as in the frame: a wrapped hint makes its tile taller
            // than its neighbours and the row stops reading as a grid.
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

// ── The compact list ────────────────────────────────────────────────────

/** The same six, one per row: small circle, label and hint together, chevron. */
@Composable
private fun CompactList(onPick: (CreateSurface) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        CreateSurface.entries.forEach { surface ->
            val shape = RoundedCornerShape(TILE_RADIUS)
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(shape)
                    .background(UsTheme.extended.bgRaised)
                    .border(EDGE, UsTheme.extended.borderMedium, shape)
                    .clickable { onPick(surface) }
                    .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l)
                    .semantics {
                        contentDescription = "Create ${surface.label.lowercase()}. ${surface.hint}"
                        role = Role.Button
                    }
                    .testTag("create-row-${surface.routeKey}"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                GlyphCircle(
                    brush = surface.brush(),
                    icon = surface.icon,
                    size = COMPACT_CIRCLE,
                    glyph = COMPACT_GLYPH,
                )
                Text(
                    text = surface.label,
                    style = MaterialTheme.typography.bodyLarge.copy(
                        fontWeight = FontWeight.Bold,
                        fontSize = LABEL_SIZE,
                    ),
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = surface.hint,
                    style = MaterialTheme.typography.bodyLarge.copy(fontSize = HINT_SIZE),
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.weight(1f),
                )
                Icon(
                    imageVector = UsIcons.ChevronRight,
                    contentDescription = null,
                    tint = UsTheme.extended.textMuted,
                    modifier = Modifier.size(CHEVRON),
                )
            }
        }
    }
}

// ── Go Live ─────────────────────────────────────────────────────────────

/**
 * Not a composer: a door to the live hub, which `:app` already registers.
 * Ember circle because ember IS the live colour; the badge says LIVE in the
 * deep end of the same ramp so the row reads as one idea.
 */
@Composable
private fun GoLiveRow(onClick: () -> Unit) {
    val shape = RoundedCornerShape(TILE_RADIUS)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgRaised)
            .border(EDGE, UsTheme.extended.borderMedium, shape)
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.xl)
            .semantics {
                contentDescription = "Go live. Broadcast instantly to your community"
                role = Role.Button
            }
            .testTag("create-go-live"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        GlyphCircle(
            brush = UsTheme.extended.create.live,
            icon = UsIcons.Radio,
            size = LIVE_CIRCLE,
            glyph = LIVE_GLYPH,
        )
        Column(modifier = Modifier.weight(1f)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(
                    text = "Go Live",
                    style = MaterialTheme.typography.bodyLarge.copy(
                        fontWeight = FontWeight.Bold,
                        fontSize = LABEL_SIZE,
                    ),
                    color = UsTheme.extended.textPrimary,
                )
                LiveBadge()
            }
            Text(
                text = "Broadcast instantly to your community",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
        }
        Icon(
            imageVector = UsIcons.ChevronRight,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(CHEVRON),
        )
    }
}

/** "● LIVE": the deep accent at 20% behind a dot and 11sp bold text. */
@Composable
private fun LiveBadge() {
    val red = UsTheme.extended.accentDeep
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(red.copy(alpha = BADGE_FILL_ALPHA))
            .padding(horizontal = UsTheme.spacing.m, vertical = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Box(
            modifier = Modifier
                .size(BADGE_DOT)
                .clip(CircleShape)
                .background(red),
        )
        Text(
            text = "LIVE",
            style = MaterialTheme.typography.labelSmall.copy(fontSize = BADGE_TEXT_SIZE),
            fontWeight = FontWeight.Bold,
            color = red,
        )
    }
}

// ── Shared pieces ───────────────────────────────────────────────────────

/** A filled gradient circle with a white glyph — every create mark on the sheet. */
@Composable
private fun GlyphCircle(
    brush: Brush,
    icon: ImageVector,
    size: androidx.compose.ui.unit.Dp,
    glyph: androidx.compose.ui.unit.Dp,
) {
    Box(
        modifier = Modifier
            .size(size)
            .clip(CircleShape)
            .background(brush),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(glyph),
        )
    }
}

/** The design's gradient for each type — tokens, never hex, in this module. */
@Composable
private fun CreateSurface.brush(): Brush {
    val create = UsTheme.extended.create
    return when (this) {
        CreateSurface.Text -> create.text
        CreateSurface.Photo -> create.photo
        CreateSurface.Reel -> create.reel
        CreateSurface.Audio -> create.audio
        CreateSurface.Poll -> create.poll
        CreateSurface.Article -> create.article
    }
}

/** Lucide: type · image · film · mic · chart-column · file-text. */
private val CreateSurface.icon: ImageVector
    get() = when (this) {
        CreateSurface.Text -> UsIcons.Type
        CreateSurface.Photo -> UsIcons.Image
        CreateSurface.Reel -> UsIcons.Film
        CreateSurface.Audio -> UsIcons.Mic
        CreateSurface.Poll -> UsIcons.Poll
        CreateSurface.Article -> UsIcons.FileText
    }

// ── Metrics, per the founder's render ───────────────────────────────────

private const val COLUMNS = 3
private const val SCRIM_ALPHA = 0.4f
private const val HANDLE_ALPHA = 0.4f
private const val PRESS_DARKEN = 0.2f
private const val BADGE_FILL_ALPHA = 0.2f

private val SHEET_RADIUS = 24.dp
private val EDGE = 1.dp
private val CONTENT_PADDING = 24.dp
private val HANDLE_WIDTH = 36.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 10.dp
private val HANDLE_BOTTOM = 14.dp
private val HEADER_GAP = 20.dp
private val GAP = 12.dp
private val TILE_RADIUS = 16.dp
private val TILE_HEIGHT = 150.dp
private val TILE_CIRCLE = 64.dp
private val TILE_GLYPH = 28.dp
private val COMPACT_CIRCLE = 36.dp
private val COMPACT_GLYPH = 18.dp
private val LIVE_CIRCLE = 48.dp
private val LIVE_GLYPH = 24.dp
private val CHEVRON = 20.dp
private val BADGE_DOT = 6.dp
private val PRESS_LIFT = 2.dp
private val PILL_RADIUS = 8.dp

private val TITLE_SIZE = 30.sp
private val PILL_TEXT_SIZE = 14.sp
private val LABEL_SIZE = 17.sp
private val HINT_SIZE = 14.sp
private val BADGE_TEXT_SIZE = 11.sp
