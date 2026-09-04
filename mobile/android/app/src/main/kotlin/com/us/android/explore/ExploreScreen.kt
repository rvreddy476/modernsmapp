package com.us.android.explore

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
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsIconSquare
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsCreateSwatch
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.ui.UnreadBadgeViewModel

/**
 * The Explore tab — Momentum's mini-app launcher (founder, 2026-09-05).
 *
 * A search field on top, then "Your apps": a 3-column grid of the launcher
 * tiles [launcherTiles] resolved from the user's module choices. Each tile
 * is the Create sheet's icon square at 56dp with its label under it; a
 * module this build cannot open yet is dimmed with a "Soon" pill and says so
 * when tapped, rather than looking inert or opening nothing.
 *
 * The field submits to [onSearch] with the typed query. There is no search
 * surface in this app yet — the only search endpoint the client knows is
 * people search for tagging — so `:app` lands the query on the search
 * placeholder, which names the scope and the query so the plumbing shows.
 *
 * [tiles] and both callbacks come from `:app`, which is the only place that
 * knows every feature a tile opens; the screen only draws and asks.
 */
@Composable
fun ExploreScreen(
    tiles: List<LauncherTile>,
    onSearch: (query: String) -> Unit,
    onOpenApp: (LauncherApp) -> Unit,
    badge: UnreadBadgeViewModel = hiltViewModel(),
) {
    val unread by badge.count.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { badge.refresh() }

    var query by rememberSaveable { mutableStateOf("") }
    var message by remember { mutableStateOf<UsMessage?>(null) }

    UsScaffold(applyPageGutter = false) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = UsTheme.spacing.pageHorizontal)
                    .padding(top = PAGE_TOP, bottom = PAGE_BOTTOM),
            ) {
                SearchField(
                    value = query,
                    onValueChange = { query = it },
                    onSubmit = { query.trim().takeIf { it.isNotEmpty() }?.let(onSearch) },
                )
                Spacer(Modifier.height(SECTION_GAP))
                Text(
                    text = "Your apps",
                    style = MaterialTheme.typography.titleMedium,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.semantics { heading() },
                )
                Spacer(Modifier.height(TITLE_GAP))
                TileGrid(
                    tiles = tiles,
                    unread = unread,
                    onOpen = { tile ->
                        if (tile.soon) {
                            message = UsMessage(comingSoonMessage(tile.app), UsMessageType.Info)
                        } else {
                            onOpenApp(tile.app)
                        }
                    },
                )
            }
            UsMessageHost(message = message, onDismiss = { message = null })
        }
    }
}

// ── Search ──────────────────────────────────────────────────────────────

/**
 * The comments composer's pill, repurposed: the raised surface, a hairline
 * border, the search glyph, and "Search Momentum" until something is
 * typed. Submits on the keyboard's search action; nothing else on the
 * page reacts to typing, because there is nothing yet to react with.
 */
@Composable
private fun SearchField(
    value: String,
    onValueChange: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    val shape = RoundedCornerShape(FIELD_RADIUS)
    val keyboard = LocalSoftwareKeyboardController.current
    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        singleLine = true,
        textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
        cursorBrush = SolidColor(UsTheme.extended.accentSolid),
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
        keyboardActions = KeyboardActions(
            onSearch = {
                keyboard?.hide()
                onSubmit()
            },
        ),
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(horizontal = FIELD_PADDING_H, vertical = FIELD_PADDING_V)
            .semantics { contentDescription = "Search Momentum" }
            .testTag("explore-search"),
        decorationBox = { inner ->
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                Icon(
                    imageVector = UsIcons.Search,
                    contentDescription = null,
                    tint = UsTheme.extended.textDim,
                    modifier = Modifier.size(FIELD_GLYPH),
                )
                Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.CenterStart) {
                    if (value.isEmpty()) {
                        Text(
                            text = "Search Momentum",
                            style = MaterialTheme.typography.bodyLarge,
                            color = UsTheme.extended.textDim,
                        )
                    }
                    inner()
                }
            }
        },
    )
}

// ── The grid ────────────────────────────────────────────────────────────

/** 3 columns, rows of tiles. Rows, not a lazy grid: nine items at most, all visible. */
@Composable
private fun TileGrid(
    tiles: List<LauncherTile>,
    unread: Int,
    onOpen: (LauncherTile) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(ROW_GAP)) {
        tiles.chunked(COLUMNS).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(TILE_GAP)) {
                row.forEach { tile ->
                    LauncherTileView(
                        tile = tile,
                        badge = if (tile.app == LauncherApp.ALERTS) unread else 0,
                        onClick = { onOpen(tile) },
                        modifier = Modifier.weight(1f),
                    )
                }
                // A short last row keeps its tiles at column width.
                repeat(COLUMNS - row.size) { Spacer(Modifier.weight(1f)) }
            }
        }
    }
}

/**
 * One tile: the app's icon square over its 13sp label. Pressed it shrinks
 * to 95% on a spring, the way an app icon gives under a finger. A "Soon"
 * tile is the same tile at 60% with the pill under its label — dimmed, not
 * disabled: it still answers a tap, with the message.
 */
@Composable
private fun LauncherTileView(
    tile: LauncherTile,
    badge: Int,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(stiffness = PRESS_STIFFNESS),
        label = "tilePress",
    )
    val app = tile.app
    Column(
        modifier = modifier
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
            }
            .clip(RoundedCornerShape(TILE_RADIUS))
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .padding(vertical = TILE_PADDING_V)
            .semantics {
                contentDescription = if (tile.soon) "${app.label}, coming soon" else "Open ${app.label}"
                role = Role.Button
            }
            .testTag("explore-tile-${app.name.lowercase()}"),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.alpha(if (tile.soon) SOON_ALPHA else 1f),
        ) {
            Box {
                UsIconSquare(swatch = app.swatch(), icon = app.icon, size = TILE_ICON, glyph = TILE_GLYPH)
                if (badge > 0) {
                    CountBadge(count = badge, modifier = Modifier.align(Alignment.TopEnd))
                }
            }
            Spacer(Modifier.height(LABEL_GAP))
            Text(
                text = app.label,
                style = MaterialTheme.typography.labelLarge,
                fontSize = LABEL_SIZE,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (tile.soon) {
            Spacer(Modifier.height(PILL_GAP))
            SoonPill()
        }
    }
}

/** "Soon": 10sp bold, muted, on the glass surface in a full-round pill. */
@Composable
private fun SoonPill() {
    Text(
        text = "Soon",
        style = MaterialTheme.typography.labelSmall,
        fontSize = PILL_TEXT,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textMuted,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.glassBg)
            .padding(horizontal = UsTheme.spacing.m, vertical = 1.dp),
    )
}

/**
 * The header bell's badge, on a tile: a white disc on the square's top-right
 * corner with the count in the deep accent, "99+" past the useful range.
 * Decorative to a screen reader — the tile's own description carries it.
 */
@Composable
private fun CountBadge(count: Int, modifier: Modifier = Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .offset(x = BADGE_OFFSET, y = -BADGE_OFFSET)
            .size(BADGE_SIZE)
            .background(Color.White, CircleShape),
    ) {
        Text(
            text = if (count > BADGE_MAX) "$BADGE_MAX+" else "$count",
            fontSize = BADGE_TEXT,
            lineHeight = BADGE_TEXT,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.accentDeep,
            maxLines = 1,
        )
    }
}

// ── Per-app presentation ────────────────────────────────────────────────

/** The design's swatch for each app — tokens, never hex, in `:app`. */
@Composable
private fun LauncherApp.swatch(): UsCreateSwatch {
    val launcher = UsTheme.extended.launcher
    return when (this) {
        LauncherApp.CHAT -> launcher.chat
        LauncherApp.FRIENDS -> launcher.friends
        LauncherApp.ALERTS -> launcher.alerts
        LauncherApp.LIVE -> launcher.live
        LauncherApp.SHOP -> launcher.shop
        LauncherApp.MATCH -> launcher.match
        LauncherApp.ASK -> launcher.ask
        LauncherApp.FEAST -> launcher.feast
        LauncherApp.TUBE -> launcher.tube
    }
}

/** Lucide: message-circle, users, bell, radio, shopping-bag, heart-handshake, circle-help, utensils, tv. */
private val LauncherApp.icon: ImageVector
    get() = when (this) {
        LauncherApp.CHAT -> UsIcons.Comment
        LauncherApp.FRIENDS -> UsIcons.Friends
        LauncherApp.ALERTS -> UsIcons.Notifications
        LauncherApp.LIVE -> UsIcons.Radio
        LauncherApp.SHOP -> UsIcons.ShoppingBag
        LauncherApp.MATCH -> UsIcons.HeartHandshake
        LauncherApp.ASK -> UsIcons.CircleHelp
        LauncherApp.FEAST -> UsIcons.Utensils
        LauncherApp.TUBE -> UsIcons.Tv
    }

// ── Metrics ─────────────────────────────────────────────────────────────

private const val COLUMNS = 3
private const val SOON_ALPHA = 0.6f
private const val PRESS_SCALE = 0.95f
private const val PRESS_STIFFNESS = 900f
private const val BADGE_MAX = 99

private val PAGE_TOP = 12.dp
private val PAGE_BOTTOM = 24.dp
private val SECTION_GAP = 24.dp
private val TITLE_GAP = 12.dp
private val HAIRLINE = 1.dp
private val FIELD_RADIUS = 22.dp
private val FIELD_PADDING_H = 16.dp
private val FIELD_PADDING_V = 12.dp
private val FIELD_GLYPH = 20.dp
private val ROW_GAP = 8.dp
private val TILE_GAP = 8.dp
private val TILE_RADIUS = 18.dp
private val TILE_PADDING_V = 12.dp
private val TILE_ICON = 56.dp
private val TILE_GLYPH = 26.dp
private val LABEL_GAP = 8.dp
private val LABEL_SIZE = 13.sp
private val PILL_GAP = 4.dp
private val PILL_TEXT = 10.sp
private val BADGE_SIZE = 18.dp
private val BADGE_OFFSET = 5.dp
private val BADGE_TEXT = 10.sp
