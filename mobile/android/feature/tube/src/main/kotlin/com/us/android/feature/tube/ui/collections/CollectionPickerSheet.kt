package com.us.android.feature.tube.ui.collections

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.ui.UsSettingsSwitchRow

/**
 * "Add to collection" (2026-10-02), opened from the pill under a long video:
 * the viewer's collections, one tap to add the video to one, and "New
 * collection" for a name and a Private switch. The web's "Save to playlist"
 * dialog as a sheet, in the idiom of every other sheet here.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CollectionPickerSheet(
    postId: String,
    onDismiss: () -> Unit,
    viewModel: CollectionPickerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(postId) { viewModel.open(postId) }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = UsTheme.extended.scrim,
        dragHandle = null,
        modifier = Modifier.testTag("collection_picker_sheet"),
    ) {
        CollectionPickerContent(state = state, onAdd = viewModel::add, onCreate = viewModel::create)
    }
}

/** The sheet's content, stateless, so it previews and tests without a ViewModel. */
@Composable
internal fun CollectionPickerContent(
    state: CollectionPickerState,
    onAdd: (VideoCollection) -> Unit,
    onCreate: (title: String, isPrivate: Boolean) -> Unit,
) {
    var creating by rememberSaveable { mutableStateOf(false) }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .imePadding()
            .padding(bottom = UsTheme.spacing.l),
    ) {
        GrabHandle()
        Text(
            text = "Add to collection",
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        )
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(max = LIST_MAX_HEIGHT)
                .verticalScroll(rememberScrollState()),
        ) {
            when {
                state.loading -> Note("Loading your collections")
                state.collections.isEmpty() && state.error == null -> Note("No collections yet. Create one below.")
                else -> state.collections.forEach { collection ->
                    val added = collection.id in state.added
                    CollectionRow(
                        collection = collection,
                        onClick = { if (!state.busy) onAdd(collection) },
                        trailing = { if (added) AddedCheck(collection.title) },
                    )
                }
            }
        }
        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.statusDanger,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
                    .testTag("collection_picker_error"),
            )
        }
        if (creating) {
            NewCollectionForm(
                busy = state.busy,
                onCancel = { creating = false },
                onCreate = { title, isPrivate ->
                    onCreate(title, isPrivate)
                    creating = false
                },
            )
        } else {
            UsSecondaryButton(
                text = "New collection",
                onClick = { creating = true },
                enabled = !state.loading,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
                    .testTag("collection_picker_new"),
            )
        }
    }
}

/** A name and a Private switch. Private is on to start with, as on the web. */
@Composable
private fun NewCollectionForm(
    busy: Boolean,
    onCancel: () -> Unit,
    onCreate: (title: String, isPrivate: Boolean) -> Unit,
) {
    var title by rememberSaveable { mutableStateOf("") }
    var isPrivate by rememberSaveable { mutableStateOf(true) }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsTextField(
            value = title,
            onValueChange = { title = it.take(TITLE_MAX) },
            label = "Collection title",
            modifier = Modifier
                .fillMaxWidth()
                .testTag("collection_picker_title"),
        )
        UsSettingsSwitchRow(
            title = "Private",
            description = "Only you can see it",
            checked = isPrivate,
            onCheckedChange = { isPrivate = it },
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsSecondaryButton(text = "Cancel", onClick = onCancel, modifier = Modifier.weight(1f))
            UsButton(
                text = "Create and add",
                onClick = { onCreate(title, isPrivate) },
                enabled = title.isNotBlank(),
                loading = busy,
                modifier = Modifier
                    .weight(1f)
                    .testTag("collection_picker_create"),
            )
        }
    }
}

@Composable
private fun AddedCheck(title: String) {
    Icon(
        imageVector = UsIcons.Check,
        contentDescription = "Added to $title",
        tint = UsTheme.extended.accent,
        modifier = Modifier.size(CHECK_GLYPH),
    )
}

@Composable
private fun Note(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xxl),
    )
}

/** 32×4, muted at 35%: a handle, not a decoration. */
@Composable
private fun GrabHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.m, bottom = UsTheme.spacing.m),
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

@Preview
@Composable
private fun CollectionPickerPreview() {
    UsTheme {
        Box(modifier = Modifier.background(UsTheme.extended.bgCardSolid)) {
            CollectionPickerContent(
                state = CollectionPickerState(
                    loading = false,
                    collections = listOf(
                        VideoCollection("c1", "Build logs", itemCount = 4, isPrivate = true, isWatchLater = false),
                        VideoCollection("c2", "Cooking", itemCount = 1, isPrivate = false, isWatchLater = false),
                    ),
                    added = setOf("c1"),
                ),
                onAdd = {},
                onCreate = { _, _ -> },
            )
        }
    }
}

private const val HANDLE_ALPHA = 0.35f
private const val TITLE_MAX = 120
private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val LIST_MAX_HEIGHT = 320.dp
private val CHECK_GLYPH = 18.dp
