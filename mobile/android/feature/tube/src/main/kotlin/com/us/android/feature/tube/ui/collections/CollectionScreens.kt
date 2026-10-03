package com.us.android.feature.tube.ui.collections

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
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.WATCH_LATER_TITLE
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.home.VideoRow
import com.us.android.feature.tube.ui.pressScale

/**
 * One collection's page (2026-10-02): Watch later, or a collection the
 * viewer made. Its videos as rows in the list's own order, each opening the
 * watch screen with the rest of the list as "Up next", and a remove glyph at
 * the end of each row. Under Tube's chrome with a back glyph and nothing lit
 * on the bar, like the saved videos.
 */
@Composable
fun CollectionScreen(
    destinations: TubeDestinations,
    viewModel: CollectionViewModel = hiltViewModel(),
) {
    val content by viewModel.content.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    RefreshOnResume(viewModel::refresh)

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            when (val current = content) {
                CollectionContent.Loading -> UsLoadingState(label = "Loading videos")
                is CollectionContent.Failed -> UsErrorState(message = current.message, onRetry = viewModel::retry)
                is CollectionContent.Ready -> CollectionBody(
                    collection = current.collection,
                    videos = current.videos,
                    thumbFor = viewModel::thumb,
                    onOpen = { item ->
                        viewModel.onOpen(current.videos)
                        destinations.onOpenVideo(item.id)
                    },
                    onRemove = viewModel::remove,
                    bottomPadding = padding,
                )
            }
            UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
        }
    }
}

/** The page is read again each time it comes back to the front: a video added on the watch screen is then here. */
@Composable
private fun RefreshOnResume(onResume: () -> Unit) {
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, onResume) {
        var first = true
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                // The ViewModel already read the list when it was made.
                if (first) first = false else onResume()
            }
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
}

@Suppress("LongParameterList")
@Composable
private fun CollectionBody(
    collection: VideoCollection,
    videos: List<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onRemove: (FeedItem) -> Unit,
    bottomPadding: PaddingValues,
) {
    if (videos.isEmpty()) {
        UsEmptyState(
            title = if (collection.isWatchLater) "Nothing in $WATCH_LATER_TITLE yet" else "No videos here yet",
            detail = if (collection.isWatchLater) {
                "Tap $WATCH_LATER_TITLE under a video and it will be kept here."
            } else {
                "Tap Add to collection under a video and choose ${collection.title}."
            },
            modifier = Modifier.testTag("tube_collection_empty"),
        )
        return
    }
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("tube_collection_list"),
        contentPadding = PaddingValues(
            top = UsTheme.spacing.l,
            bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
        ),
    ) {
        item(key = "title") { CollectionTitle(collection = collection, count = videos.size) }
        items(videos, key = { it.id }) { item ->
            CollectionVideoRow(
                item = item,
                thumb = thumbFor(item),
                listTitle = collection.title,
                onOpen = { onOpen(item) },
                onRemove = { onRemove(item) },
            )
        }
    }
}

/** The list's name, and "N videos · Private" under it. */
@Composable
private fun CollectionTitle(collection: VideoCollection, count: Int) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(bottom = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = collection.title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.testTag("tube_collection_title"),
        )
        Text(
            text = collectionMeta(count, collection.isPrivate),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** "1 video · Private", "12 videos · Public". */
fun collectionMeta(count: Int, isPrivate: Boolean): String =
    "$count ${if (count == 1) "video" else "videos"} · ${if (isPrivate) "Private" else "Public"}"

/** The "Up next" row, with the remove glyph at its end. */
@Composable
private fun CollectionVideoRow(
    item: FeedItem,
    thumb: VideoThumb,
    listTitle: String,
    onOpen: () -> Unit,
    onRemove: () -> Unit,
) {
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        VideoRow(item = item, thumb = thumb, onClick = onOpen, modifier = Modifier.weight(1f))
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .padding(end = UsTheme.spacing.m)
                .size(REMOVE_TARGET)
                .pressScale(onRemove)
                .semantics {
                    role = Role.Button
                    contentDescription = "Remove from $listTitle"
                }
                .testTag("tube_collection_remove:${item.id}"),
        ) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(REMOVE_GLYPH),
            )
        }
    }
}

/**
 * "Collections" (2026-10-02): the lists the viewer made, each a row that
 * opens its page. Made from the watch page's "Add to collection".
 */
@Composable
fun CollectionsScreen(
    destinations: TubeDestinations,
    viewModel: CollectionsViewModel = hiltViewModel(),
) {
    val content by viewModel.content.collectAsStateWithLifecycle()
    RefreshOnResume(viewModel::refresh)

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        when (val current = content) {
            CollectionsContent.Loading -> UsLoadingState(label = "Loading collections")
            is CollectionsContent.Failed -> UsErrorState(message = current.message, onRetry = viewModel::retry)
            is CollectionsContent.Ready -> if (current.collections.isEmpty()) {
                UsEmptyState(
                    title = "No collections yet",
                    detail = "Tap Add to collection under a video to make your first one.",
                    modifier = Modifier.testTag("tube_collections_empty"),
                )
            } else {
                LazyColumn(
                    modifier = Modifier
                        .fillMaxSize()
                        .testTag("tube_collections_list"),
                    contentPadding = PaddingValues(
                        top = UsTheme.spacing.l,
                        bottom = padding.calculateBottomPadding() + UsTheme.spacing.xxl,
                    ),
                ) {
                    item(key = "title") {
                        Text(
                            text = "Collections",
                            style = MaterialTheme.typography.titleLarge,
                            fontWeight = FontWeight.Bold,
                            color = UsTheme.extended.textPrimary,
                            modifier = Modifier
                                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                                .padding(bottom = UsTheme.spacing.m),
                        )
                    }
                    items(current.collections, key = { it.id }) { collection ->
                        CollectionRow(
                            collection = collection,
                            onClick = { destinations.onOpenCollection(collection.id) },
                        )
                    }
                }
            }
        }
    }
}

/** One collection: a folder, its name, "N videos · Private", a chevron. */
@Composable
internal fun CollectionRow(
    collection: VideoCollection,
    onClick: () -> Unit,
    /** What sits at the row's end; the chevron when nothing is given. */
    trailing: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = collection.title
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("tube_collection:${collection.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(
            imageVector = UsIcons.Folder,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = collection.title,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = collectionMeta(collection.itemCount, collection.isPrivate),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        if (trailing != null) {
            trailing()
        } else {
            Icon(
                imageVector = UsIcons.ChevronRight,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(REMOVE_GLYPH),
            )
        }
    }
}

@Preview
@Composable
private fun CollectionRowPreview() {
    UsTheme {
        CollectionRow(
            collection = VideoCollection(
                id = "c1",
                title = "Build logs",
                itemCount = 4,
                isPrivate = true,
                isWatchLater = false,
            ),
            onClick = {},
        )
    }
}

private val REMOVE_TARGET = 40.dp
private val REMOVE_GLYPH = 18.dp
private val ROW_GLYPH = 22.dp
