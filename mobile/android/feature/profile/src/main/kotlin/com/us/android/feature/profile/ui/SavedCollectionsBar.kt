package com.us.android.feature.profile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.saved.MAX_COLLECTION_NAME_LENGTH
import com.us.android.core.feed.data.saved.SavedCollection

/** What the Saved tab's collection controls can ask for. */
internal class SavedCollectionActions(
    val onSelect: (String?) -> Unit,
    val onCreate: () -> Unit,
    val onRename: (SavedCollection) -> Unit,
    val onDelete: (SavedCollection) -> Unit,
    val onRetry: () -> Unit,
)

/**
 * The Saved tab's collections (2026-10-24), over its tiles: a pill per
 * collection — All Saved first — with its count, then "New collection".
 * The collection on screen, when it is one of the viewer's, offers Rename
 * and Delete under the pills. A long press on a tile moves that post.
 */
@Composable
internal fun SavedCollectionsBar(state: SavedTabUiState, actions: SavedCollectionActions) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s), modifier = Modifier.testTag("saved_collections")) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            state.collections.forEach { collection ->
                CollectionPill(
                    label = "${collection.name} · ${collection.count}",
                    spoken = "${collection.name}, ${savedCountLabel(collection.count)}",
                    active = collection.id == state.selected,
                    onClick = { actions.onSelect(collection.id) },
                    tag = "saved_collection:${collection.id ?: "default"}",
                )
            }
            if (state.canCreate) {
                CollectionPill(
                    label = "New collection",
                    spoken = "New collection",
                    active = false,
                    onClick = actions.onCreate,
                    tag = "saved_collection_new",
                    icon = UsIcons.FolderPlus,
                )
            }
        }
        val selected = state.selectedCollection
        if (!selected.isDefault) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                TextButton(onClick = { actions.onRename(selected) }, modifier = Modifier.testTag("saved_collection_rename")) {
                    Text("Rename", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textPrimary)
                }
                TextButton(onClick = { actions.onDelete(selected) }, modifier = Modifier.testTag("saved_collection_delete")) {
                    Text("Delete", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.error)
                }
            }
        }
        state.loadError?.let { error ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = actions.onRetry) { Text("Retry", style = MaterialTheme.typography.labelLarge) }
            }
        }
        state.notice?.let { notice ->
            Text(
                text = notice,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.testTag("saved_collection_notice"),
            )
        }
    }
}

/** One glass pill, white with navy text when [active] — the grid tabs' look, a size smaller. */
@Suppress("LongParameterList")
@Composable
private fun CollectionPill(
    label: String,
    spoken: String,
    active: Boolean,
    onClick: () -> Unit,
    tag: String,
    icon: ImageVector? = null,
) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .height(PILL_HEIGHT)
            .background(if (active) UsTheme.extended.selectedPill else UsTheme.extended.glassBg, shape)
            .border(HAIRLINE, if (active) UsTheme.extended.selectedPill else UsTheme.extended.glassBorder, shape)
            .clickable(onClick = onClick)
            .semantics {
                role = Role.Tab
                selected = active
                contentDescription = spoken
            }
            .padding(horizontal = PILL_HORIZONTAL)
            .testTag(tag),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            icon?.let {
                Icon(
                    it,
                    contentDescription = null,
                    tint = if (active) UsTheme.extended.onSelectedPill else UsTheme.extended.textPrimary,
                    modifier = Modifier.size(PILL_GLYPH),
                )
            }
            Text(
                text = label,
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = if (active) UsTheme.extended.onSelectedPill else UsTheme.extended.textPrimary,
                maxLines = 1,
            )
        }
    }
}

/**
 * The Saved tab's open dialog: a name for New or Rename, or the delete
 * question — what happens to the posts is said before it happens.
 */
@Composable
internal fun SavedCollectionDialog(
    dialog: SavedDialog,
    busy: Boolean,
    onName: (String) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    val (title, confirm) = when (dialog) {
        is SavedDialog.Create -> "New collection" to "Create"
        is SavedDialog.Rename -> "Rename collection" to "Save"
        is SavedDialog.ConfirmDelete -> "Delete “${dialog.collection.name}”?" to "Delete"
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                when (dialog) {
                    is SavedDialog.Create -> NameField(dialog.name, dialog.problem, busy, onName)
                    is SavedDialog.Rename -> NameField(dialog.name, dialog.problem, busy, onName)
                    is SavedDialog.ConfirmDelete -> {
                        Text(deleteQuestion(dialog.collection))
                        dialog.problem?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onConfirm, enabled = !busy, modifier = Modifier.testTag("saved_dialog_confirm")) {
                Text(
                    if (busy) "Working…" else confirm,
                    color = if (dialog is SavedDialog.ConfirmDelete) MaterialTheme.colorScheme.error else UsTheme.extended.textPrimary,
                )
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, enabled = !busy) { Text("Cancel") }
        },
        modifier = Modifier.testTag("saved_dialog"),
    )
}

@Composable
private fun NameField(name: String, problem: String?, busy: Boolean, onName: (String) -> Unit) {
    UsTextField(
        value = name,
        onValueChange = onName,
        label = "Collection name",
        placeholder = "Up to $MAX_COLLECTION_NAME_LENGTH characters",
        errorText = problem,
        enabled = !busy,
        modifier = Modifier
            .fillMaxWidth()
            .testTag("saved_dialog_name"),
    )
}

/** "1 post", "12 posts". */
internal fun savedCountLabel(count: Long): String = if (count == 1L) "1 post" else "$count posts"

private val HAIRLINE = 1.dp
private val PILL_HEIGHT = 30.dp
private val PILL_HORIZONTAL = 12.dp
private val PILL_GLYPH = 14.dp

/** The bar's actions, bound to the Saved tab's ViewModel. */
internal fun savedCollectionActions(viewModel: SavedCollectionsViewModel) = SavedCollectionActions(
    onSelect = viewModel::select,
    onCreate = viewModel::startCreate,
    onRename = viewModel::startRename,
    onDelete = viewModel::startDelete,
    onRetry = viewModel::load,
)

/** What an empty collection says: All Saved keeps the tab's words, a collection its own name. */
internal fun savedEmptyTitle(state: SavedTabUiState): String? =
    state.selectedCollection.takeIf { !it.isDefault }?.let { "Nothing in ${it.name} yet" }
