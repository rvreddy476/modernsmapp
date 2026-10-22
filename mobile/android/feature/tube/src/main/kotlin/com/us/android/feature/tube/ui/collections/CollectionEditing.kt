package com.us.android.feature.tube.ui.collections

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.CollectionVisibility
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsSettingsRadioRow
import com.us.android.feature.tube.ui.home.displayTitle
import com.us.android.feature.tube.ui.pressScale

/** Where a row of a collection may move, given its [index] among [count] rows: the moves offered, in order. */
enum class CollectionRowMove(val label: String) {
    TOP("Move to top"),
    UP("Move up"),
    DOWN("Move down"),
    BOTTOM("Move to bottom"),
    ;

    /** The visible index the row lands on. */
    fun target(index: Int, count: Int): Int = when (this) {
        TOP -> 0
        UP -> index - 1
        DOWN -> index + 1
        BOTTOM -> count - 1
    }
}

/** The moves that do something for a row at [index] of [count]: none at an edge that would not move it. Pure. */
fun collectionRowMoves(index: Int, count: Int): List<CollectionRowMove> = buildList {
    if (index > 0) {
        if (index > 1) add(CollectionRowMove.TOP)
        add(CollectionRowMove.UP)
    }
    if (index in 0 until count - 1) {
        add(CollectionRowMove.DOWN)
        if (index < count - 2) add(CollectionRowMove.BOTTOM)
    }
}

/**
 * A row's ⋮ in a collection the viewer made (web parity, 2026-10-22): the
 * keyboard path of the web's drag — Move to top, Move up, Move down, Move to
 * bottom — and Remove. A row acts and the sheet closes; the move itself is
 * optimistic on the page beneath.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun CollectionRowSheet(
    collection: VideoCollection,
    item: FeedItem,
    index: Int,
    count: Int,
    onMove: (toIndex: Int) -> Unit,
    onRemove: () -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_CORNER, topEnd = SHEET_CORNER),
        scrimColor = UsTheme.extended.scrim,
        dragHandle = null,
        modifier = Modifier.testTag("tube_collection_row_sheet:${item.id}"),
    ) {
        Column(modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l)) {
            GrabHandle()
            Text(
                text = item.displayTitle,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
            )
            collectionRowMoves(index, count).forEach { move ->
                SheetRow(
                    icon = UsIcons.ChevronDown,
                    flip = move == CollectionRowMove.TOP || move == CollectionRowMove.UP,
                    label = move.label,
                    testTag = "tube_collection_move:${move.name.lowercase()}",
                    onClick = {
                        onDismiss()
                        onMove(move.target(index, count))
                    },
                )
            }
            SheetRow(
                icon = UsIcons.Trash,
                label = "Remove from ${collection.title}",
                tint = UsTheme.extended.statusDanger,
                testTag = "tube_collection_row_remove",
                onClick = {
                    onDismiss()
                    onRemove()
                },
            )
        }
    }
}

@Suppress("LongParameterList")
@Composable
private fun SheetRow(
    icon: ImageVector,
    label: String,
    testTag: String,
    onClick: () -> Unit,
    flip: Boolean = false,
    tint: Color = UsTheme.extended.textPrimary,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = ROW_HEIGHT)
            .pressScale(onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag(testTag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = tint,
            modifier = Modifier.size(ROW_GLYPH).let { if (flip) it.rotate(FLIPPED) else it },
        )
        Text(text = label, style = MaterialTheme.typography.bodyLarge, color = tint)
    }
}

/**
 * The details of a collection the viewer made (web parity, 2026-10-22):
 * title, description and who can see it — Private, Unlisted or Public, the
 * server's three. Save sends only what changed and waits for the server;
 * the sheet stays open, with what was typed, if it refuses.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun CollectionEditSheet(
    collection: VideoCollection,
    saving: Boolean,
    onSave: (title: String, description: String, visibility: CollectionVisibility) -> Unit,
    onDismiss: () -> Unit,
) {
    var title by rememberSaveable(collection.id) { mutableStateOf(collection.title) }
    var description by rememberSaveable(collection.id) { mutableStateOf(collection.description) }
    var visibility by rememberSaveable(collection.id) { mutableStateOf(collection.visibility) }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_CORNER, topEnd = SHEET_CORNER),
        scrimColor = UsTheme.extended.scrim,
        dragHandle = null,
        modifier = Modifier.testTag("tube_collection_edit_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .imePadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            GrabHandle()
            Text(
                text = "Edit collection",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
            UsTextField(
                value = title,
                onValueChange = { title = it.take(COLLECTION_TITLE_MAX) },
                label = "Title",
                errorText = if (title.isBlank()) "A collection needs a title." else null,
                enabled = !saving,
                modifier = Modifier.fillMaxWidth().testTag("tube_collection_edit_title"),
            )
            UsTextField(
                value = description,
                onValueChange = { description = it.take(COLLECTION_DESCRIPTION_MAX) },
                label = "Description",
                singleLine = false,
                enabled = !saving,
                modifier = Modifier.fillMaxWidth().testTag("tube_collection_edit_description"),
            )
            Text(
                text = "Who can see it",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textSecondary,
            )
            VISIBILITY_ORDER.forEach { option ->
                UsSettingsRadioRow(
                    title = option.label,
                    selected = visibility == option,
                    onClick = { visibility = option },
                    description = visibilityDetail(option),
                    enabled = !saving,
                    modifier = Modifier.testTag("tube_collection_edit_visibility:${option.wire}"),
                )
            }
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsSecondaryButton(text = "Cancel", onClick = onDismiss, enabled = !saving, modifier = Modifier.weight(1f))
                UsButton(
                    text = "Save",
                    onClick = { onSave(title, description, visibility) },
                    enabled = title.isNotBlank(),
                    loading = saving,
                    modifier = Modifier.weight(1f).testTag("tube_collection_edit_save"),
                )
            }
        }
    }
}

/** What each visibility means, in the server's terms (`playlistReadableBy`). */
internal fun visibilityDetail(visibility: CollectionVisibility): String = when (visibility) {
    CollectionVisibility.PRIVATE -> "Only you can see it"
    CollectionVisibility.UNLISTED -> "Anyone with the link, not listed on your channel"
    CollectionVisibility.PUBLIC -> "Anyone, and listed on your channel"
}

/** "Delete “Build logs”?": the list goes, its videos stay. Asked first: it cannot be undone. */
@Composable
internal fun DeleteCollectionDialog(title: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = UsTheme.extended.bgSheet,
        titleContentColor = UsTheme.extended.textPrimary,
        textContentColor = UsTheme.extended.textSecondary,
        title = { Text(text = "Delete “$title”?", style = MaterialTheme.typography.titleMedium) },
        text = {
            Text(
                text = "The collection is removed. The videos in it are not deleted.",
                style = MaterialTheme.typography.bodyMedium,
            )
        },
        confirmButton = {
            UsPillButton(text = "Delete", onClick = onConfirm, modifier = Modifier.testTag("tube_collection_delete_confirm"))
        },
        dismissButton = { UsPillButton(text = "Cancel", onClick = onDismiss, filled = false) },
    )
}

private val VISIBILITY_ORDER = listOf(CollectionVisibility.PRIVATE, CollectionVisibility.UNLISTED, CollectionVisibility.PUBLIC)
private const val FLIPPED = 180f
private val SHEET_CORNER = 28.dp
private val ROW_HEIGHT = 52.dp
private val ROW_GLYPH = 20.dp
