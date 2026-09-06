package com.us.android.feature.post.createhub

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
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
import com.us.android.core.designsystem.component.UsIconSquare
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsCreateSwatch
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.launch

/**
 * The Create sheet — what the bar's "+" opens, over whatever is on screen.
 *
 * ## CHOOSE ONCE, THEN COMPOSE
 *
 * Six typed tiles and a Go Live row. Picking a tile closes the sheet and
 * pushes the composer for exactly that type; there is no rail inside the
 * composer to switch afterwards. A sheet puts the choice on top of where the
 * user already is, so backing out costs nothing.
 *
 * ## SMALL, ONE LOOK (founder, 2026-09-04)
 *
 * The first cut was a full-height panel: 64dp circles, a 30sp title and a
 * "Compact view" toggle offering a second layout. The founder asked for a
 * short sheet with small, polished icons and one look. So: a 40dp rounded
 * square per type — the app-icon idiom — with its gradient, a gloss across
 * the top half and a coloured glow beneath, a 13sp label, and nothing else in
 * the tile. The grid is 3×2 at 84dp a row; the whole sheet clears in about a
 * third of the screen. The compact list is gone with the toggle, and with it
 * the preference that remembered it.
 *
 * ## WHAT IS NOT HERE
 *
 * No "Drafts" link. The server keeps `post_drafts`, but this app has no drafts
 * screen or route to open — the composer restores its ONE durable draft on
 * its own (`ComposerDraftStore`). A link to a list that does not exist would
 * be dead UI, so it is omitted rather than stubbed.
 *
 * ## WHAT IT OFFERS IS THE SCOPE'S, NOT THE SHEET'S (2026-09-06)
 *
 * [scope] decides which tiles are drawn and whether Go Live is under them —
 * see [CreateScope], which is the ONE place that mapping lives. In Tube the
 * plus offers three things and nothing else; everywhere else it is the full
 * sheet. The sheet itself never asks where it is.
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
    scope: CreateScope = CreateScope.App,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val sheetScope = rememberCoroutineScope()

    // Slide the sheet away FIRST, then act: navigating while the sheet is
    // still up would leave the composer arriving under a scrim.
    fun leaveThen(action: () -> Unit) {
        sheetScope.launch { sheetState.hide() }.invokeOnCompletion {
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
        // The handle is drawn inside the content so it sits under the rim
        // highlight rather than above it.
        dragHandle = null,
        modifier = Modifier.testTag("create-sheet"),
    ) {
        val rim = Color.White.copy(alpha = RIM_ALPHA)
        val rimWidth = with(LocalDensity.current) { HAIRLINE.toPx() }
        Column(
            modifier = Modifier
                .fillMaxWidth()
                // A one-pixel light rim along the top edge: the sheet reads
                // as a lifted pane catching light, not a flat block.
                .drawBehind {
                    drawLine(
                        color = rim,
                        start = Offset(0f, rimWidth / 2),
                        end = Offset(size.width, rimWidth / 2),
                        strokeWidth = rimWidth,
                    )
                }
                .padding(horizontal = CONTENT_PADDING)
                .padding(bottom = CONTENT_BOTTOM)
                .navigationBarsPadding(),
        ) {
            GrabHandle()
            SheetHeader(onClose = { leaveThen {} })
            Spacer(Modifier.height(HEADER_GAP))
            TileGrid(surfaces = scope.surfaces, onPick = { leaveThen { onPick(it) } })
            if (scope.offersLive) {
                Spacer(Modifier.height(GAP))
                GoLiveRow(onClick = { leaveThen(onOpenLive) })
            }
        }
    }
}

// ── Header ──────────────────────────────────────────────────────────────

/** 32×4, muted at 35%: a handle, not a decoration. */
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

/** "Create" on the left, a small glass close button on the right. */
@Composable
private fun SheetHeader(onClose: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "Create",
            style = MaterialTheme.typography.titleLarge.copy(fontSize = TITLE_SIZE),
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Box(
            modifier = Modifier
                .size(CLOSE_BUTTON)
                .clip(CircleShape)
                .background(UsTheme.extended.glassBg)
                .clickable(onClick = onClose)
                .semantics {
                    contentDescription = "Close"
                    role = Role.Button
                }
                .testTag("create-close"),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(CLOSE_GLYPH),
            )
        }
    }
}

// ── The grid ────────────────────────────────────────────────────────────

/**
 * Up to 4 columns, 8dp gaps. Rows, not a lazy grid: a handful of items, all
 * visible. Four across since Video joined Reel (2026-09-05) — three would
 * have left one tile alone on a third row. A short last row is padded with
 * empty slots so every tile keeps the same width, which is also what keeps
 * Tube's two tiles the size of the app sheet's rather than half the screen
 * each.
 */
@Composable
private fun TileGrid(surfaces: List<CreateSurface>, onPick: (CreateSurface) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(GAP)) {
        surfaces.chunked(COLUMNS).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(GAP)) {
                row.forEach { surface ->
                    CreateTile(
                        surface = surface,
                        onClick = { onPick(surface) },
                        modifier = Modifier.weight(1f),
                    )
                }
                repeat(COLUMNS - row.size) { Spacer(Modifier.weight(1f)) }
            }
        }
    }
}

/**
 * One tile: the type's icon square over its label, on a faint glass card.
 * Pressed it shrinks to 95% on a spring — the tap reads as the tile giving
 * under a finger, the way an app icon does.
 */
@Composable
private fun CreateTile(surface: CreateSurface, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(stiffness = PRESS_STIFFNESS),
        label = "tilePress",
    )
    val shape = RoundedCornerShape(TILE_RADIUS)

    Column(
        modifier = modifier
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
            }
            .height(TILE_HEIGHT)
            .clip(shape)
            .background(Color.White.copy(alpha = TILE_FILL_ALPHA))
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .semantics {
                contentDescription = "Create ${surface.label.lowercase()}. ${surface.hint}"
                role = Role.Button
            }
            .testTag("create-tile-${surface.routeKey}"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        UsIconSquare(swatch = surface.swatch(), icon = surface.icon, size = TILE_ICON, glyph = TILE_GLYPH)
        Spacer(Modifier.height(TILE_LABEL_GAP))
        Text(
            text = surface.label,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

// ── Go Live ─────────────────────────────────────────────────────────────

/**
 * Not a composer: a door to the live hub, which `:app` already registers.
 * Ember because ember IS the live colour; the badge says LIVE in the deep end
 * of the same ramp so the row reads as one idea.
 */
@Composable
private fun GoLiveRow(onClick: () -> Unit) {
    val shape = RoundedCornerShape(TILE_RADIUS)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Color.White.copy(alpha = TILE_FILL_ALPHA))
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .clickable(onClick = onClick)
            .padding(horizontal = LIVE_PADDING_H, vertical = LIVE_PADDING_V)
            .semantics {
                contentDescription = "Go live. Broadcast to your community"
                role = Role.Button
            }
            .testTag("create-go-live"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsIconSquare(
            swatch = UsTheme.extended.create.live,
            icon = UsIcons.Radio,
            size = LIVE_ICON,
            glyph = LIVE_GLYPH,
        )
        Column(modifier = Modifier.weight(1f)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(
                    text = "Go Live",
                    style = MaterialTheme.typography.titleMedium,
                    color = UsTheme.extended.textPrimary,
                )
                LiveBadge()
            }
            Text(
                text = "Broadcast to your community",
                style = MaterialTheme.typography.bodySmall,
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

/** "● LIVE": the deep accent at 18% behind a dot and 10sp bold text. */
@Composable
private fun LiveBadge() {
    val red = UsTheme.extended.accentDeep
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(red.copy(alpha = BADGE_FILL_ALPHA))
            .padding(horizontal = UsTheme.spacing.s, vertical = 1.dp),
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
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Bold,
            color = red,
        )
    }
}

// ── Shared pieces ───────────────────────────────────────────────────────

/** The design's swatch for each type — tokens, never hex, in this module. */
@Composable
private fun CreateSurface.swatch(): UsCreateSwatch {
    val create = UsTheme.extended.create
    return when (this) {
        CreateSurface.Text -> create.text
        CreateSurface.Photo -> create.photo
        CreateSurface.Reel -> create.reel
        // Tube's own red, so the tile and the launcher tile it posts to match.
        CreateSurface.Video -> UsTheme.extended.launcher.tube
        CreateSurface.Audio -> create.audio
        CreateSurface.Poll -> create.poll
        CreateSurface.Article -> create.article
    }
}

/** Lucide: type · image · film · clapperboard · mic · chart-column · file-text. */
private val CreateSurface.icon: ImageVector
    get() = when (this) {
        CreateSurface.Text -> UsIcons.Type
        CreateSurface.Photo -> UsIcons.Image
        CreateSurface.Reel -> UsIcons.Film
        CreateSurface.Video -> UsIcons.Clapperboard
        CreateSurface.Audio -> UsIcons.Mic
        CreateSurface.Poll -> UsIcons.Poll
        CreateSurface.Article -> UsIcons.FileText
    }

// ── Metrics ─────────────────────────────────────────────────────────────

private const val COLUMNS = 4
private const val SCRIM_ALPHA = 0.55f
private const val RIM_ALPHA = 0.08f
private const val HANDLE_ALPHA = 0.35f
private const val TILE_FILL_ALPHA = 0.04f
private const val BADGE_FILL_ALPHA = 0.18f
private const val PRESS_SCALE = 0.95f
private const val PRESS_STIFFNESS = 900f

private val SHEET_RADIUS = 28.dp
private val HAIRLINE = 1.dp
private val CONTENT_PADDING = 16.dp
private val CONTENT_BOTTOM = 12.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
private val HANDLE_BOTTOM = 12.dp
private val HEADER_GAP = 14.dp
private val GAP = 8.dp
private val TILE_RADIUS = 18.dp
private val TILE_HEIGHT = 84.dp
private val TILE_ICON = 40.dp
private val TILE_GLYPH = 19.dp
private val TILE_LABEL_GAP = 8.dp
private val CLOSE_BUTTON = 30.dp
private val CLOSE_GLYPH = 14.dp
private val LIVE_ICON = 36.dp
private val LIVE_GLYPH = 17.dp
private val LIVE_PADDING_H = 12.dp
private val LIVE_PADDING_V = 10.dp
private val CHEVRON = 16.dp
private val BADGE_DOT = 5.dp

private val TITLE_SIZE = 20.sp
