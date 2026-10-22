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
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
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
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.CollectionVisibility
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
import com.us.android.feature.tube.ui.home.displayTitle
import com.us.android.feature.tube.ui.pressScale

/**
 * One collection's page (2026-10-02): Watch later, Liked videos
 * (2026-10-22), or a collection the viewer made. Its videos as rows in the
 * list's own order, each opening the watch screen with the rest of the list
 * as "Up next". Under Tube's chrome with a back glyph and nothing lit on the
 * bar, like the saved videos.
 *
 * What a row and the header offer follows whose list it is
 * ([collectionPermissions], the web's `CollectionView`): a list the viewer
 * made has Edit and Delete under its title and a ⋮ on each row with the
 * moves and Remove; Watch later has the × alone; Liked videos is read-only,
 * because the like is its only writer.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionScreen(
    destinations: TubeDestinations,
    viewModel: CollectionViewModel = hiltViewModel(),
) {
    val content by viewModel.content.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    val edit by viewModel.edit.collectAsStateWithLifecycle()
    val confirmingDelete by viewModel.confirmingDelete.collectAsStateWithLifecycle()
    val deleting by viewModel.deleting.collectAsStateWithLifecycle()
    val deleted by viewModel.deleted.collectAsStateWithLifecycle()
    var rowMenuFor by rememberSaveable { mutableStateOf<String?>(null) }
    RefreshOnResume(viewModel::refresh)

    // Deleted: the page has nothing left to show. The Collections index reads again on resume.
    LaunchedEffect(deleted) {
        if (deleted) destinations.onBack()
    }

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        val refreshing by viewModel.refreshing.collectAsStateWithLifecycle()
        // Pull to refresh (UI audit 2026-10-21): the rows stay while the list is read again.
        PullToRefreshBox(isRefreshing = refreshing, onRefresh = viewModel::refresh, modifier = Modifier.fillMaxSize()) {
            when (val current = content) {
                CollectionContent.Loading -> UsLoadingState(label = "Loading videos")
                is CollectionContent.Failed -> UsErrorState(message = current.message, onRetry = viewModel::retry)
                is CollectionContent.Ready -> CollectionBody(
                    collection = current.collection,
                    videos = current.videos,
                    actions = CollectionPageActions(
                        thumbFor = viewModel::thumb,
                        open = { item ->
                            viewModel.onOpen(current.videos)
                            destinations.onOpenVideo(item.id)
                        },
                        remove = viewModel::remove,
                        rowMenu = { item -> rowMenuFor = item.id },
                        edit = viewModel::openEdit,
                        delete = viewModel::askDelete,
                        deleting = deleting,
                    ),
                    bottomPadding = padding,
                )
            }
            UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
        }
    }

    val ready = content as? CollectionContent.Ready
    val rowItem = ready?.videos?.firstOrNull { it.id == rowMenuFor }
    if (ready != null && rowItem != null) {
        CollectionRowSheet(
            collection = ready.collection,
            item = rowItem,
            index = ready.videos.indexOf(rowItem),
            count = ready.videos.size,
            onMove = { to -> viewModel.move(rowItem, to) },
            onRemove = { viewModel.remove(rowItem) },
            onDismiss = { rowMenuFor = null },
        )
    }
    if (ready != null && edit != CollectionEditState.CLOSED) {
        CollectionEditSheet(
            collection = ready.collection,
            saving = edit == CollectionEditState.SAVING,
            onSave = viewModel::save,
            onDismiss = viewModel::closeEdit,
        )
    }
    if (ready != null && confirmingDelete) {
        DeleteCollectionDialog(
            title = ready.collection.title,
            onConfirm = viewModel::delete,
            onDismiss = viewModel::cancelDelete,
        )
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

/** Everything the page's rows and header can ask, grouped, so the body stays short. */
private class CollectionPageActions(
    val thumbFor: (FeedItem) -> VideoThumb,
    val open: (FeedItem) -> Unit,
    val remove: (FeedItem) -> Unit,
    val rowMenu: (FeedItem) -> Unit,
    val edit: () -> Unit,
    val delete: () -> Unit,
    val deleting: Boolean,
)

@Composable
private fun CollectionBody(
    collection: VideoCollection,
    videos: List<FeedItem>,
    actions: CollectionPageActions,
    bottomPadding: PaddingValues,
) {
    val permissions = collectionPermissions(collection)
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("tube_collection_list"),
        contentPadding = PaddingValues(
            top = UsTheme.spacing.l,
            bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
        ),
    ) {
        item(key = "title") {
            CollectionTitle(collection = collection, count = videos.size, permissions = permissions, actions = actions)
        }
        if (videos.isEmpty()) {
            item(key = "empty") {
                Box(modifier = Modifier.fillParentMaxHeight(EMPTY_HEIGHT)) {
                    UsEmptyState(
                        title = emptyTitle(collection),
                        detail = emptyDetail(collection),
                        modifier = Modifier.testTag("tube_collection_empty"),
                    )
                }
            }
        }
        items(videos, key = { it.id }) { item ->
            CollectionVideoRow(
                item = item,
                thumb = actions.thumbFor(item),
                listTitle = collection.title,
                permissions = permissions,
                onOpen = { actions.open(item) },
                onRemove = { actions.remove(item) },
                onMenu = { actions.rowMenu(item) },
                modifier = if (UsTheme.reducedMotion) Modifier else Modifier.animateItem(),
            )
        }
    }
}

private fun emptyTitle(collection: VideoCollection): String = when {
    collection.isWatchLater -> "Nothing in $WATCH_LATER_TITLE yet"
    collection.isLiked -> "Videos you like will show up here"
    else -> "No videos here yet"
}

private fun emptyDetail(collection: VideoCollection): String = when {
    collection.isWatchLater -> "Tap $WATCH_LATER_TITLE under a video and it will be kept here."
    collection.isLiked -> "Tap Like on any video to keep it."
    else -> "Tap Add to collection under a video and choose ${collection.title}."
}

/** The list's name, "N videos · Private" under it, its description, and — for a list the viewer made — Edit and Delete. */
@Composable
private fun CollectionTitle(
    collection: VideoCollection,
    count: Int,
    permissions: CollectionPermissions,
    actions: CollectionPageActions,
) {
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
            text = if (collection.isSystem) "$count ${if (count == 1) "video" else "videos"} · Only you" else collectionMeta(count, collection.visibility),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        if (collection.description.isNotBlank()) {
            Text(
                text = collection.description,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                maxLines = DESCRIPTION_LINES,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.testTag("tube_collection_description"),
            )
        }
        if (permissions.canEdit) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier.padding(top = UsTheme.spacing.s),
            ) {
                UsPillButton(
                    text = "Edit",
                    onClick = actions.edit,
                    filled = false,
                    modifier = Modifier.testTag("tube_collection_edit"),
                )
                UsPillButton(
                    text = "Delete",
                    onClick = actions.delete,
                    filled = false,
                    busy = actions.deleting,
                    modifier = Modifier.testTag("tube_collection_delete"),
                )
            }
        }
    }
}

/** "1 video · Private", "12 videos · Public". */
fun collectionMeta(count: Int, isPrivate: Boolean): String =
    collectionMeta(count, if (isPrivate) CollectionVisibility.PRIVATE else CollectionVisibility.PUBLIC)

/** "1 video · Private", "3 videos · Unlisted", "12 videos · Public". */
fun collectionMeta(count: Int, visibility: CollectionVisibility): String =
    "$count ${if (count == 1) "video" else "videos"} · ${visibility.label}"

/** The "Up next" row, with what the list allows at its end: ⋮ (moves and Remove), ×, or nothing. */
@Suppress("LongParameterList")
@Composable
private fun CollectionVideoRow(
    item: FeedItem,
    thumb: VideoThumb,
    listTitle: String,
    permissions: CollectionPermissions,
    onOpen: () -> Unit,
    onRemove: () -> Unit,
    onMenu: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(modifier = modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        VideoRow(item = item, thumb = thumb, onClick = onOpen, modifier = Modifier.weight(1f))
        when {
            permissions.canReorder -> RowGlyphButton(
                icon = UsIcons.More,
                description = "More for ${item.displayTitle}",
                onClick = onMenu,
                testTag = "tube_collection_more:${item.id}",
            )
            permissions.canRemove -> RowGlyphButton(
                icon = UsIcons.Close,
                description = "Remove from $listTitle",
                onClick = onRemove,
                testTag = "tube_collection_remove:${item.id}",
            )
        }
    }
}

@Composable
private fun RowGlyphButton(
    icon: ImageVector,
    description: String,
    onClick: () -> Unit,
    testTag: String,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .padding(end = UsTheme.spacing.m)
            .size(REMOVE_TARGET)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .testTag(testTag),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(REMOVE_GLYPH),
        )
    }
}

private const val DESCRIPTION_LINES = 3
private const val EMPTY_HEIGHT = 0.6f


/**
 * "Collections" (2026-10-02): the lists the viewer made, each a row that
 * opens its page. Made from the watch page's "Add to collection".
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionsScreen(
    destinations: TubeDestinations,
    viewModel: CollectionsViewModel = hiltViewModel(),
) {
    val content by viewModel.content.collectAsStateWithLifecycle()
    RefreshOnResume(viewModel::refresh)

    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        val refreshing by viewModel.refreshing.collectAsStateWithLifecycle()
        // Pull to refresh (UI audit 2026-10-21): the rows stay while the list is read again.
        PullToRefreshBox(isRefreshing = refreshing, onRefresh = viewModel::refresh, modifier = Modifier.fillMaxSize()) {
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
