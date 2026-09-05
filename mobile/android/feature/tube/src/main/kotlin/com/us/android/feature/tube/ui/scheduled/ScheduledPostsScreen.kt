package com.us.android.feature.tube.ui.scheduled

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.ui.schedule.ScheduleSheet
import com.us.android.core.media.publish.ScheduleWindow
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.pressScale
import java.time.ZoneId

/**
 * "Scheduled posts" (header More, 2026-09-05): every post of the viewer's
 * that is waiting to go live, soonest first, each with its still, its
 * title, when it goes out, and two pills — Reschedule, which opens the
 * same picker the reel form uses, and Publish now. Under Tube's chrome
 * with a back glyph and nothing lit on the bar: this is not one of its
 * pages. A tapped row opens the post in the watch screen when it is a
 * video; other kinds have no viewer inside Tube, so the row is read-only.
 */
@Composable
fun ScheduledPostsScreen(
    destinations: TubeDestinations,
    viewModel: ScheduledViewModel = hiltViewModel(),
) {
    val items by viewModel.items.collectAsStateWithLifecycle()
    val busy by viewModel.busy.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    var moving by remember { mutableStateOf<FeedItem?>(null) }

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            ScheduledBody(
                items = items,
                busy = busy,
                stillFor = viewModel::still,
                onOpen = { item -> if (item.feedContentType == LONG_VIDEO) destinations.onOpenVideo(item.id) },
                onReschedule = { moving = it },
                onPublishNow = viewModel::publishNow,
                bottomPadding = padding,
            )
            UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
        }
    }

    moving?.let { item ->
        ScheduleSheet(
            initial = ScheduleWindow.parse(item.publishAt),
            onSchedule = { at ->
                viewModel.reschedule(item, at)
                moving = null
            },
            onClear = {
                viewModel.publishNow(item)
                moving = null
            },
            onDismiss = { moving = null },
        )
    }
}

@Composable
private fun ScheduledBody(
    items: List<FeedItem>?,
    busy: Set<String>,
    stillFor: (FeedItem) -> String?,
    onOpen: (FeedItem) -> Unit,
    onReschedule: (FeedItem) -> Unit,
    onPublishNow: (FeedItem) -> Unit,
    bottomPadding: PaddingValues,
) {
    when {
        items == null -> UsLoadingState(label = "Loading scheduled posts")
        items.isEmpty() -> UsEmptyState(
            title = "Nothing scheduled",
            detail = "Posts you schedule from the create hub wait here until they go live.",
            modifier = Modifier.testTag("tube_scheduled_empty"),
        )
        else -> LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .testTag("tube_scheduled_list"),
            contentPadding = PaddingValues(
                start = UsTheme.spacing.pageHorizontal,
                end = UsTheme.spacing.pageHorizontal,
                top = UsTheme.spacing.l,
                bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
            ),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item(key = "title") { PageTitle() }
            items(items, key = { it.id }) { item ->
                ScheduledRow(
                    item = item,
                    still = stillFor(item),
                    busy = item.id in busy,
                    onOpen = { onOpen(item) },
                    onReschedule = { onReschedule(item) },
                    onPublishNow = { onPublishNow(item) },
                )
            }
        }
    }
}

@Composable
private fun PageTitle() {
    Text(
        text = "Scheduled posts",
        style = MaterialTheme.typography.titleLarge,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(bottom = UsTheme.spacing.s),
    )
}

/** A glass card: the still on the left, the words on the right, the two pills under them. */
@Composable
private fun ScheduledRow(
    item: FeedItem,
    still: String?,
    busy: Boolean,
    onOpen: () -> Unit,
    onReschedule: () -> Unit,
    onPublishNow: () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val zone = remember { ZoneId.systemDefault() }
    val at = ScheduleWindow.parse(item.publishAt)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(UsTheme.spacing.l)
            .testTag("tube_scheduled_row:${item.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .pressScale(onOpen),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Still(url = still)
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = item.title.ifBlank { item.text }.ifBlank { "Untitled post" },
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                ) {
                    Icon(
                        imageVector = UsIcons.Clock,
                        contentDescription = null,
                        tint = UsTheme.extended.textMuted,
                        modifier = Modifier.size(CLOCK_GLYPH),
                    )
                    Text(
                        text = at?.let { "Goes live ${ScheduleWindow.label(it, zone)}" } ?: "Scheduled",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsPillButton(
                text = "Reschedule",
                onClick = onReschedule,
                filled = false,
                enabled = !busy,
                modifier = Modifier.testTag("tube_scheduled_move:${item.id}"),
            )
            UsPillButton(
                text = "Publish now",
                onClick = onPublishNow,
                busy = busy,
                modifier = Modifier.testTag("tube_scheduled_publish:${item.id}"),
            )
        }
    }
}

/** The 16:9 still, or the raised tile with a clock when the post has no picture. */
@Composable
private fun Still(url: String?) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Box(
        modifier = Modifier
            .size(width = STILL_WIDTH, height = STILL_HEIGHT)
            .clip(shape)
            .background(UsTheme.extended.bgRaised),
        contentAlignment = Alignment.Center,
    ) {
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Icon(
                imageVector = UsIcons.FileText,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(PLACEHOLDER_GLYPH),
            )
        }
    }
}

private const val LONG_VIDEO = "long_video"
private val HAIRLINE = 1.dp
private val STILL_WIDTH = 96.dp
private val STILL_HEIGHT = 54.dp
private val CLOCK_GLYPH = 14.dp
private val PLACEHOLDER_GLYPH = 22.dp
