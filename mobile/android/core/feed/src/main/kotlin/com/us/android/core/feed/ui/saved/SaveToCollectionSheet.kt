package com.us.android.core.feed.ui.saved

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.saved.MAX_COLLECTION_NAME_LENGTH
import com.us.android.core.feed.data.saved.SavedCollection
import com.us.android.core.feed.ui.edit.OwnerSheet

/**
 * The collection picker (saved collections, 2026-10-24; the web's
 * SaveToCollectionSheet): All Saved, the viewer's collections with their
 * counts, and "New collection" — created, then picked. "Save to collection"
 * from a post's More menu, "Move to collection" from a saved post on the
 * Saved tab. The collection a saved post is in now is marked and not
 * offered as a move.
 *
 * [onDone] gets the outcome once the server has it; the sheet has nothing
 * left to show then, and the host says the sentence where the viewer is.
 */
@Composable
fun SaveToCollectionSheet(
    target: CollectionTarget,
    onDone: (CollectionOutcome) -> Unit,
    onDismiss: () -> Unit,
    viewModel: SaveToCollectionViewModel = hiltViewModel(key = "save-to-collection:${target.postId}"),
) {
    LaunchedEffect(target) { viewModel.bind(target) }
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.outcome) {
        state.outcome?.let { outcome ->
            viewModel.consumeOutcome()
            onDone(outcome)
        }
    }
    OwnerSheet(
        title = target.title,
        subtitle = target.subtitle,
        icon = UsIcons.FolderPlus,
        testTag = "save_to_collection_sheet",
        onDismiss = { if (!state.busy) onDismiss() },
    ) {
        when {
            state.loading -> Text(
                text = "Loading your collections…",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            state.loadError != null -> {
                SheetError(state.loadError.orEmpty())
                UsSecondaryButton(text = "Try again", onClick = viewModel::load, modifier = Modifier.fillMaxWidth())
            }
            else -> CollectionChoices(state, viewModel)
        }
    }
}

@Composable
private fun CollectionChoices(state: SaveToCollectionUiState, viewModel: SaveToCollectionViewModel) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        state.collections.forEach { collection ->
            val current = state.isCurrent(collection)
            CollectionChoiceRow(
                collection = collection,
                current = current,
                enabled = !state.busy && !current,
                onClick = { viewModel.pick(collection) },
            )
        }
    }
    if (state.naming) {
        UsTextField(
            value = state.name,
            onValueChange = viewModel::changeName,
            label = "Collection name",
            placeholder = "Up to $MAX_COLLECTION_NAME_LENGTH characters",
            errorText = state.nameProblem,
            enabled = !state.busy,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("save_to_collection_name"),
        )
        UsButton(
            text = "Create and ${if (state.moving) "move" else "save"}",
            onClick = viewModel::createAndPick,
            loading = state.busy,
            enabled = state.name.isNotBlank(),
            modifier = Modifier
                .fillMaxWidth()
                .testTag("save_to_collection_create"),
        )
    } else if (state.canCreate) {
        ChoiceRow(
            icon = UsIcons.FolderPlus,
            title = "New collection",
            detail = null,
            current = false,
            enabled = !state.busy,
            onClick = viewModel::startNaming,
            tag = "save_to_collection_new",
        )
    }
    state.error?.let { SheetError(it) }
}

@Composable
private fun CollectionChoiceRow(collection: SavedCollection, current: Boolean, enabled: Boolean, onClick: () -> Unit) {
    ChoiceRow(
        icon = if (collection.isDefault) UsIcons.BookmarkOutline else UsIcons.Folder,
        title = collection.name,
        detail = collectionCountLabel(collection.count) + if (current) " · Here now" else "",
        current = current,
        enabled = enabled,
        onClick = onClick,
        tag = "save_to_collection_row:${collection.id ?: "default"}",
    )
}

/** One row of the picker: a glyph in a soft square, the name, the count, a check on the current one. */
@Suppress("LongParameterList")
@Composable
private fun ChoiceRow(
    icon: ImageVector,
    title: String,
    detail: String?,
    current: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    tag: String,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .clickable(enabled = enabled, role = Role.Button, onClickLabel = title, onClick = onClick)
            .semantics { selected = current }
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .size(ICON_SQUARE)
                .background(UsTheme.extended.fillSubtle, RoundedCornerShape(UsTheme.radii.medium)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(imageVector = icon, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(GLYPH))
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            detail?.let {
                Text(text = it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary, maxLines = 1)
            }
        }
        if (current) {
            Icon(
                imageVector = UsIcons.Check,
                contentDescription = "Here now",
                tint = UsTheme.extended.accentStrong,
                modifier = Modifier.size(GLYPH),
            )
        }
    }
}

@Composable
private fun SheetError(message: String) {
    Text(
        text = message,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier
            .padding(top = UsTheme.spacing.s)
            .testTag("save_to_collection_error"),
    )
}

/** "1 post", "12 posts". */
fun collectionCountLabel(count: Long): String = if (count == 1L) "1 post" else "$count posts"

private val ROW_HEIGHT = 56.dp
private val ICON_SQUARE = 40.dp
private val GLYPH = 20.dp
