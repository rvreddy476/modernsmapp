package com.us.android.core.feed.ui.edit

import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.AUDIO_LANGUAGE_CHOICES
import com.us.android.core.feed.data.EditChoice
import com.us.android.core.feed.data.PostEditChoices
import com.us.android.core.feed.data.PostEditForm
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow
import com.us.android.core.ui.UsSettingsSwitchRow

/**
 * "Edit" — the owner's edit of their own reel or long video (2026-10-21,
 * the web's Edit sheet), opened from the video's More menu. The fields the
 * web's Details form offers: title, description, category, tags, hashtags,
 * language, visibility, comments and who may comment, offline copies, and
 * "More settings" (license, embedding, remix, moderation, the comment sort,
 * the recording date and place, paid promotion, like count; and on a long
 * video made for kids, the age restriction and altered content, which a
 * short does not ask at upload either).
 *
 * Save sends ONLY what moved. [onSaved] fires once the server has it; the
 * lists already show the new words by then (`PostEdits`).
 */
@Composable
fun PostEditSheet(
    postId: String,
    longVideo: Boolean,
    onDismiss: () -> Unit,
    onSaved: () -> Unit = {},
    viewModel: PostEditViewModel = hiltViewModel(key = "post-edit:$postId"),
) {
    LaunchedEffect(postId) { viewModel.bind(postId, longVideo) }
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.saved) {
        if (state.saved) {
            onSaved()
            onDismiss()
        }
    }

    OwnerSheet(
        title = if (longVideo) "Edit video" else "Edit reel",
        subtitle = "Title, description, topic, visibility",
        icon = UsIcons.Pencil,
        testTag = "post_edit_sheet",
        onDismiss = onDismiss,
    ) {
        when {
            state.loading -> Text(
                text = "Loading…",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            state.loadError != null -> {
                SheetNote(text = state.loadError.orEmpty(), isError = true, testTag = "post_edit_load_error")
                UsSecondaryButton(text = "Try again", onClick = viewModel::retry, modifier = Modifier.fillMaxWidth())
            }
            else -> {
                EditFields(state = state, change = viewModel::change)
                state.problems.forEach { SheetNote(text = it, isError = true, testTag = "post_edit_problem") }
                UsButton(
                    text = "Save",
                    onClick = viewModel::save,
                    enabled = state.dirty,
                    loading = state.saving,
                    modifier = Modifier.fillMaxWidth().testTag("post_edit_save"),
                )
            }
        }
    }
}

/** The Details fields, then "More settings". */
@Composable
private fun ColumnScope.EditFields(state: PostEditUiState, change: ((PostEditForm) -> PostEditForm) -> Unit) {
    val form = state.form
    UsTextField(
        value = form.title,
        onValueChange = { value -> change { it.copy(title = value) } },
        label = "Title",
        modifier = Modifier.fillMaxWidth().testTag("post_edit_title"),
    )
    UsTextField(
        value = form.text,
        onValueChange = { value -> change { it.copy(text = value) } },
        label = "Description",
        singleLine = false,
        modifier = Modifier.fillMaxWidth().testTag("post_edit_text"),
    )
    UsTextField(
        value = form.hashtags,
        onValueChange = { value -> change { it.copy(hashtags = value) } },
        label = "Hashtags",
        placeholder = "travel, food",
        modifier = Modifier.fillMaxWidth().testTag("post_edit_hashtags"),
    )
    UsTextField(
        value = form.tags,
        onValueChange = { value -> change { it.copy(tags = value) } },
        label = "Tags",
        placeholder = "Separated by commas",
        modifier = Modifier.fillMaxWidth().testTag("post_edit_tags"),
    )
    UsSettingsSelectRow(
        title = "Category",
        selected = form.category,
        options = listOf(UsSettingsOption("", "None")) +
            state.categories.map { UsSettingsOption(it.id, it.label) } +
            listOfNotNull(
                // A category the list no longer carries is still shown as itself.
                form.category.takeIf { c -> c.isNotBlank() && state.categories.none { it.id == c } }
                    ?.let { UsSettingsOption(it, it) },
            ),
        onSelected = { value -> change { it.copy(category = value) } },
    )
    ChoiceRow("Visibility", form.visibility, PostEditChoices.visibility) { value ->
        change { it.copy(visibility = value) }
    }
    UsSettingsSelectRow(
        title = "Language",
        selected = form.language,
        options = listOf(UsSettingsOption("", "Not set")) +
            AUDIO_LANGUAGE_CHOICES.map { UsSettingsOption(it.tag, it.label) },
        onSelected = { value -> change { it.copy(language = value) } },
    )
    UsSettingsSwitchRow(
        title = "Comments",
        checked = !form.noComments,
        onCheckedChange = { on -> change { it.copy(noComments = !on) } },
    )
    ChoiceRow("Who can comment", form.commentAccess, PostEditChoices.commentAccess) { value ->
        change { it.copy(commentAccess = value) }
    }
    UsSettingsSwitchRow(
        title = "Save offline",
        description = "Viewers can keep a copy inside the app to watch with no network.",
        checked = form.allowDownload,
        onCheckedChange = { on -> change { it.copy(allowDownload = on) } },
    )
    MoreSettings(state = state, change = change)
}

@Composable
private fun MoreSettings(state: PostEditUiState, change: ((PostEditForm) -> PostEditForm) -> Unit) {
    val form = state.form
    UsSettingsSection(title = "More settings") {
        if (state.longVideo) {
            UsSettingsSwitchRow(
                title = "Made for kids",
                checked = form.madeForKids,
                onCheckedChange = { on -> change { it.copy(madeForKids = on) } },
            )
            UsSettingsSwitchRow(
                title = "Age restriction (18+)",
                checked = form.ageRestricted,
                onCheckedChange = { on -> change { it.copy(ageRestricted = on) } },
            )
            UsSettingsSwitchRow(
                title = "Altered or AI content",
                checked = form.alteredContent,
                onCheckedChange = { on -> change { it.copy(alteredContent = on) } },
            )
        }
        UsSettingsSwitchRow(
            title = "Paid promotion",
            checked = form.paidPromotion,
            onCheckedChange = { on -> change { it.copy(paidPromotion = on) } },
        )
        UsSettingsSwitchRow(
            title = "Hide like count",
            checked = form.hideLikeCount,
            onCheckedChange = { on -> change { it.copy(hideLikeCount = on) } },
        )
        UsSettingsSwitchRow(
            title = "Allow embedding",
            checked = form.allowEmbedding,
            onCheckedChange = { on -> change { it.copy(allowEmbedding = on) } },
        )
        ChoiceRow("License", form.license, PostEditChoices.license) { value -> change { it.copy(license = value) } }
        ChoiceRow("Remixing", form.remixSetting, PostEditChoices.remix) { value ->
            change { it.copy(remixSetting = value) }
        }
        ChoiceRow("Comment moderation", form.commentModeration, PostEditChoices.commentModeration) { value ->
            change { it.copy(commentModeration = value) }
        }
        ChoiceRow("Comments open on", form.defaultCommentSort, PostEditChoices.commentSort) { value ->
            change { it.copy(defaultCommentSort = value) }
        }
        UsTextField(
            value = form.recordingDate,
            onValueChange = { value -> change { it.copy(recordingDate = value) } },
            label = "Recording date",
            placeholder = "YYYY-MM-DD",
            modifier = Modifier.fillMaxWidth().testTag("post_edit_recording_date"),
        )
        UsTextField(
            value = form.recordingLocation,
            onValueChange = { value -> change { it.copy(recordingLocation = value) } },
            label = "Recording location",
            modifier = Modifier.fillMaxWidth().testTag("post_edit_recording_location"),
        )
    }
}

/** An enum field: its current choice at the right, the list on a tap. A value outside the list still reads as itself. */
@Composable
private fun ChoiceRow(title: String, selected: String, choices: List<EditChoice>, onSelected: (String) -> Unit) {
    UsSettingsSelectRow(
        title = title,
        selected = selected,
        options = choices.map { UsSettingsOption(it.wire, it.label) },
        onSelected = onSelected,
    )
}
