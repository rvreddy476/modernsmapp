package com.us.android.feature.tube.ui.offline

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.offline.OfflineKind
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsSwitchRow
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.pressScale
import java.io.File

/**
 * "Offline" (2026-10-02): the videos and reels this device keeps to watch
 * with no network.
 *
 * founder, 2026-10-02: "It should be like to see offline in the app only
 * like YouTube, TikTok or Instagram. Nobody can see download location." So
 * the page lists copies by title, never by file: there is no path, no "open
 * with", no share, and nothing here leaves the app. A row plays the copy in
 * the app's own player (the watch screen for a long video, Reels for a
 * reel); the glyph at its end removes it.
 *
 * Above the list: how much room the copies take, "Remove all", and the
 * Wi-Fi-only switch. The page reads the device alone, so it opens the same
 * with or without a connection.
 */
@Composable
fun OfflineScreen(
    destinations: TubeDestinations,
    viewModel: OfflineViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val wifiOnly by viewModel.wifiOnly.collectAsStateWithLifecycle()
    val notice by viewModel.notice.collectAsStateWithLifecycle()
    var confirmRemoveAll by rememberSaveable { mutableStateOf(false) }

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            if (!state.loaded) {
                UsLoadingState(label = "Loading offline copies")
            } else {
                OfflineList(
                    sections = offlineSections(state),
                    usedBytes = state.usedBytes,
                    wifiOnly = wifiOnly,
                    bottomPadding = padding,
                    actions = OfflineActions(
                        onOpen = { postId ->
                            when (viewModel.onOpen(postId)) {
                                OfflineKind.VIDEO -> destinations.onOpenVideo(postId)
                                OfflineKind.REEL -> destinations.onOpenReels()
                                null -> Unit
                            }
                        },
                        onRemove = viewModel::remove,
                        onRemoveAll = { confirmRemoveAll = true },
                        onWifiOnlyChange = viewModel::setWifiOnly,
                    ),
                )
            }
            UsMessageHost(
                message = notice,
                onDismiss = viewModel::dismissNotice,
            )
        }
    }

    if (confirmRemoveAll) {
        RemoveAllDialog(
            onConfirm = {
                confirmRemoveAll = false
                viewModel.removeAll()
            },
            onDismiss = { confirmRemoveAll = false },
        )
    }
}

/** What the page can ask for. A bundle, so the list takes one parameter for its four actions. */
internal class OfflineActions(
    val onOpen: (postId: String) -> Unit,
    val onRemove: (postId: String) -> Unit,
    val onRemoveAll: () -> Unit,
    val onWifiOnlyChange: (Boolean) -> Unit,
)

@Composable
internal fun OfflineList(
    sections: OfflineSections,
    usedBytes: Long,
    wifiOnly: Boolean,
    bottomPadding: PaddingValues,
    actions: OfflineActions,
) {
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("tube_offline_list"),
        contentPadding = PaddingValues(
            top = UsTheme.spacing.l,
            bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
        ),
    ) {
        item(key = "header") {
            OfflineHeader(
                count = sections.count,
                usedBytes = usedBytes,
                wifiOnly = wifiOnly,
                onRemoveAll = actions.onRemoveAll,
                onWifiOnlyChange = actions.onWifiOnlyChange,
            )
        }
        if (sections.isEmpty) {
            item(key = "empty") {
                UsEmptyState(
                    title = "Nothing saved offline",
                    detail = "Open More on a video or a reel and tap Save offline to watch it here with no connection.",
                    modifier = Modifier
                        .padding(top = EMPTY_TOP)
                        .testTag("tube_offline_empty"),
                )
            }
        }
        offlineSection(title = "Videos", rows = sections.videos, actions = actions)
        offlineSection(title = "Reels", rows = sections.reels, actions = actions)
    }
}

private fun LazyListScope.offlineSection(title: String, rows: List<OfflineRow>, actions: OfflineActions) {
    if (rows.isEmpty()) return
    item(key = "section:$title") {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(top = UsTheme.spacing.xl, bottom = UsTheme.spacing.s)
                .semantics { heading() }
                .testTag("tube_offline_section:${title.lowercase()}"),
        )
    }
    items(rows, key = { it.postId }) { row ->
        OfflineCopyRow(row = row, onOpen = { actions.onOpen(row.postId) }, onRemove = { actions.onRemove(row.postId) })
    }
}

/** The page's name, what the copies take, "Remove all", and the Wi-Fi-only switch. */
@Composable
private fun OfflineHeader(
    count: Int,
    usedBytes: Long,
    wifiOnly: Boolean,
    onRemoveAll: () -> Unit,
    onWifiOnlyChange: (Boolean) -> Unit,
) {
    Column(modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
        Text(
            text = "Offline",
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(top = UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = offlineStorageLabel(count, usedBytes),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier
                    .weight(1f)
                    .testTag("tube_offline_storage"),
            )
            if (count > 0) {
                UsPillButton(
                    text = "Remove all",
                    onClick = onRemoveAll,
                    filled = false,
                    modifier = Modifier.testTag("tube_offline_remove_all"),
                )
            }
        }
        UsSettingsSwitchRow(
            title = "Save on Wi-Fi only",
            description = "Videos you save offline wait for Wi-Fi instead of using mobile data.",
            checked = wifiOnly,
            onCheckedChange = onWifiOnlyChange,
            modifier = Modifier.testTag("tube_offline_wifi_only"),
        )
    }
}

/**
 * One copy: its stored still, its title, "channel · size · expiry" (or how
 * the save is going), and the glyph that removes it. A copy still being
 * saved is not a button: there is nothing to play yet.
 */
@Composable
private fun OfflineCopyRow(row: OfflineRow, onOpen: () -> Unit, onRemove: () -> Unit) {
    val open = if (row.stored) Modifier.pressScale(onOpen) else Modifier
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .then(open)
            .semantics {
                if (row.stored) role = Role.Button
                contentDescription = if (row.stored) "Play ${row.title}" else "${row.title}. ${row.meta}"
            }
            .padding(start = UsTheme.spacing.pageHorizontal, top = UsTheme.spacing.s, bottom = UsTheme.spacing.s)
            .testTag("tube_offline_row:${row.postId}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        OfflineStill(row)
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = row.title,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = row.meta,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (!row.stored) SavingBar(row.progress)
        }
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .padding(end = UsTheme.spacing.m)
                .size(REMOVE_TARGET)
                .pressScale(onRemove)
                .semantics {
                    role = Role.Button
                    contentDescription = if (row.stored) "Remove offline copy" else "Cancel offline save"
                }
                .testTag("tube_offline_remove:${row.postId}"),
        ) {
            Icon(
                imageVector = if (row.stored) UsIcons.Trash else UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(REMOVE_GLYPH),
            )
        }
    }
}

/** The stored still: 16:9 for a long video, upright for a reel. The wash alone while there is none. */
@Composable
private fun OfflineStill(row: OfflineRow) {
    val reel = row.kind == OfflineKind.REEL
    Box(
        modifier = Modifier
            .width(if (reel) REEL_STILL_WIDTH else VIDEO_STILL_WIDTH)
            .aspectRatio(if (reel) REEL_RATIO else VIDEO_RATIO)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.fillSubtle),
    ) {
        row.posterFile?.let { path ->
            AsyncImage(
                model = File(path),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

@Composable
private fun SavingBar(progress: Float?) {
    val modifier = Modifier
        .fillMaxWidth()
        .padding(top = UsTheme.spacing.xs, end = UsTheme.spacing.l)
    if (progress == null) {
        LinearProgressIndicator(
            color = UsTheme.extended.accentSolid,
            trackColor = UsTheme.extended.fillSubtle,
            modifier = modifier,
        )
    } else {
        LinearProgressIndicator(
            progress = { progress.coerceIn(0f, 1f) },
            color = UsTheme.extended.accentSolid,
            trackColor = UsTheme.extended.fillSubtle,
            modifier = modifier,
        )
    }
}

/** "Remove all offline copies?": every copy leaves the device at once, so it is asked first. */
@Composable
private fun RemoveAllDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = UsTheme.extended.bgSheet,
        titleContentColor = UsTheme.extended.textPrimary,
        textContentColor = UsTheme.extended.textSecondary,
        title = { Text(text = "Remove all offline copies?", style = MaterialTheme.typography.titleMedium) },
        text = {
            Text(
                text = "Every video and reel saved on this device will be removed. You can save them again later.",
                style = MaterialTheme.typography.bodyMedium,
            )
        },
        confirmButton = {
            UsPillButton(
                text = "Remove all",
                onClick = onConfirm,
                modifier = Modifier.testTag("tube_offline_remove_all_confirm"),
            )
        },
        dismissButton = { UsPillButton(text = "Cancel", onClick = onDismiss, filled = false) },
    )
}

private const val VIDEO_RATIO = 16f / 9f
private const val REEL_RATIO = 9f / 16f
private val VIDEO_STILL_WIDTH = 128.dp
private val REEL_STILL_WIDTH = 54.dp
private val REMOVE_TARGET = 40.dp
private val REMOVE_GLYPH = 18.dp
private val EMPTY_TOP = 48.dp

@Preview
@Composable
private fun OfflineListPreview() {
    UsTheme {
        Box(Modifier.background(UsTheme.extended.bgCanvas)) {
            OfflineList(
                sections = OfflineSections(
                    videos = listOf(
                        OfflineRow(
                            postId = "p1",
                            kind = OfflineKind.VIDEO,
                            title = "Friday build",
                            channelName = "Raghu Builds",
                            posterFile = null,
                            stored = true,
                            progress = null,
                            meta = "Raghu Builds · 175 MB · Expires in 12 days",
                        ),
                    ),
                    reels = listOf(
                        OfflineRow(
                            postId = "p2",
                            kind = OfflineKind.REEL,
                            title = "Monsoon walk, my take",
                            channelName = "Raghu Builds",
                            posterFile = null,
                            stored = false,
                            progress = 0.42f,
                            meta = "Raghu Builds · Saving 42%",
                        ),
                    ),
                ),
                usedBytes = 184_320_000L,
                wifiOnly = true,
                bottomPadding = PaddingValues(),
                actions = OfflineActions(onOpen = {}, onRemove = {}, onRemoveAll = {}, onWifiOnlyChange = {}),
            )
        }
    }
}
