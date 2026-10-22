package com.us.android.explore

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.ui.UnreadBadgeViewModel

/** The mini-app directory; navigation and module availability still belong to :app. */
@Composable
fun ExploreScreen(
    tiles: List<LauncherTile>,
    onSearch: (String) -> Unit,
    onOpenApp: (LauncherApp) -> Unit,
    /** Null on the bar's Explore root, which has no back arrow; set when Explore was pushed. */
    onBack: (() -> Unit)?,
    badge: UnreadBadgeViewModel = hiltViewModel(),
) {
    val unread by badge.count.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { badge.refresh() }
    var query by rememberSaveable { mutableStateOf("") }
    var message by remember { mutableStateOf<UsMessage?>(null) }

    UsScaffold(topBar = { UsTopBar("Explore", onBack = onBack) }, applyPageGutter = false) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            ExploreContent(
                tiles = tiles,
                unread = unread,
                query = query,
                onQueryChange = { query = it },
                onSearch = { query.trim().takeIf(String::isNotEmpty)?.let(onSearch) },
                onOpen = { tile ->
                    if (tile.soon) {
                        message = UsMessage(comingSoonMessage(tile.app), UsMessageType.Info)
                    } else {
                        onOpenApp(tile.app)
                    }
                },
            )
            UsMessageHost(message = message, onDismiss = { message = null })
        }
    }
}

/** Stateless presentation so the real screen can be previewed without auth or network. */
@Composable
internal fun ExploreContent(
    tiles: List<LauncherTile>,
    unread: Int,
    query: String,
    onQueryChange: (String) -> Unit,
    onSearch: () -> Unit,
    onOpen: (LauncherTile) -> Unit,
) {
    val sections = remember(tiles, query) { exploreSections(tiles, query) }
    BoxWithConstraints(Modifier.fillMaxSize(), contentAlignment = Alignment.TopCenter) {
        val columns = if (maxWidth >= TABLET_BREAKPOINT) TABLET_COLUMNS else PHONE_COLUMNS
        LazyColumn(
            modifier = Modifier.widthIn(max = 840.dp).fillMaxSize().testTag("explore-directory"),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(24.dp),
        ) {
            item(key = "intro") {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(
                        "Your day, connected.",
                        style = MaterialTheme.typography.headlineMedium,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.semantics { heading() },
                    )
                    Text(
                        "People, stories and everyday essentials. All here.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textMuted,
                    )
                    Spacer(Modifier.height(4.dp))
                    SearchField(query, onQueryChange, onSearch)
                }
            }
            sections.forEach { section ->
                item(key = section.title) {
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        Text(
                            section.title,
                            style = MaterialTheme.typography.titleMedium,
                            color = UsTheme.extended.textPrimary,
                            modifier = Modifier.semantics { heading() },
                        )
                        section.tiles.chunked(columns).forEach { row ->
                            Row(
                                modifier = Modifier.height(IntrinsicSize.Max),
                                horizontalArrangement = Arrangement.spacedBy(12.dp),
                            ) {
                                row.forEach { tile ->
                                    AppTile(tile, unread, { onOpen(tile) }, Modifier.weight(1f).fillMaxHeight())
                                }
                                repeat(columns - row.size) { Spacer(Modifier.weight(1f)) }
                            }
                        }
                    }
                }
            }
            if (sections.isEmpty()) {
                item(key = "empty") {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text("No apps found", style = MaterialTheme.typography.titleMedium)
                        Text(
                            "Try Feast, shopping or rides. Use your keyboard's search action to search beyond apps.",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun SearchField(value: String, onValueChange: (String) -> Unit, onSubmit: () -> Unit) {
    val shape = RoundedCornerShape(16.dp)
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
        modifier = Modifier.fillMaxWidth()
            .background(UsTheme.extended.bgRaised, shape)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .padding(horizontal = 16.dp, vertical = 16.dp)
            .semantics { contentDescription = "Search apps or search VChat" }
            .testTag("explore-search"),
        decorationBox = { inner ->
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Icon(UsIcons.Search, null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(20.dp))
                Box(Modifier.weight(1f)) {
                    if (value.isEmpty()) {
                        Text(
                            "Find an app or service",
                            style = MaterialTheme.typography.bodyLarge,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                    inner()
                }
            }
        },
    )
}

@Composable
private fun AppTile(tile: LauncherTile, unread: Int, onClick: () -> Unit, modifier: Modifier) {
    val app = tile.app
    val shape = RoundedCornerShape(20.dp)
    Column(
        modifier = modifier.clip(shape)
            .background(UsTheme.extended.bgCardSolid)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(16.dp)
            .semantics(mergeDescendants = true) {
                contentDescription = buildString {
                    append(if (tile.soon) "${app.label}, coming soon" else "Open ${app.label}")
                    if (app == LauncherApp.ALERTS && unread > 0) append(", $unread unread")
                }
            }
            .testTag("explore-tile-${app.name.lowercase()}"),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(
            Modifier.size(44.dp).background(UsTheme.extended.unreadRow, RoundedCornerShape(14.dp)),
            contentAlignment = Alignment.Center,
        ) {
            UsBadgedIcon(app.icon, if (app == LauncherApp.ALERTS) unread else 0, tint = UsTheme.extended.accentSolid)
        }
        Text(app.label, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        Text(
            if (tile.soon) "Coming soon" else app.description,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

private const val PHONE_COLUMNS = 2
private const val TABLET_COLUMNS = 3
private val TABLET_BREAKPOINT = 600.dp

private val LauncherApp.icon: ImageVector
    get() = when (this) {
        LauncherApp.CHAT -> UsIcons.Comment
        LauncherApp.FRIENDS -> UsIcons.Friends
        LauncherApp.ALERTS -> UsIcons.Notifications
        LauncherApp.LIVE -> UsIcons.Radio
        LauncherApp.MSTORE -> UsIcons.ShoppingBag
        LauncherApp.MSELLER -> UsIcons.Store
        LauncherApp.MATCH -> UsIcons.HeartHandshake
        LauncherApp.ASK -> UsIcons.CircleHelp
        LauncherApp.FEAST -> UsIcons.Utensils
        LauncherApp.TUBE -> UsIcons.Tv
        LauncherApp.RIDE -> UsIcons.Gauge
        LauncherApp.DOORSTEP -> UsIcons.Wrench
    }

@Preview(showBackground = true, widthDp = 360, heightDp = 800)
@Composable
private fun ExplorePreview() {
    UsTheme { ExploreContent(launcherTiles(), 0, "", {}, {}, {}) }
}
