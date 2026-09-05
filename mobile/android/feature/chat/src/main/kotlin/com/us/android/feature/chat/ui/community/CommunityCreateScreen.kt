package com.us.android.feature.chat.ui.community

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunityRules
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.home.rememberMediaUrl

/**
 * Create a community, or edit one: picture, name, handle (live validity and
 * the server's own "taken" answer), about, visibility. The same form for
 * both — an edit simply arrives filled and keeps its handle.
 */
@Composable
fun CommunityCreateScreen(
    onSaved: (Community) -> Unit,
    onBack: () -> Unit,
    viewModel: CommunityCreateViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.saved) { state.saved?.let(onSaved) }

    UsScaffold(
        topBar = { UsTopBar(title = if (state.isEdit) "Edit community" else "New community", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        if (state.loading) {
            UsLoadingState(label = "Loading community", modifier = Modifier.padding(padding))
            return@UsScaffold
        }
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            AvatarPicker(state = state, onPicked = viewModel::onAvatarPicked)
            CommunityFields(state = state, viewModel = viewModel)
            FormError(state.error)
            UsButton(
                text = when {
                    state.submitting -> if (state.isEdit) "Saving…" else "Creating…"
                    state.isEdit -> "Save"
                    else -> "Create community"
                },
                onClick = viewModel::submit,
                enabled = state.canSubmit,
                loading = state.submitting,
                modifier = Modifier.fillMaxWidth().testTag("community_submit"),
            )
        }
    }
}

/** The picture slot: the picked file, else the current avatar, else initials. */
@Composable
private fun AvatarPicker(state: CommunityFormUiState, onPicked: (android.net.Uri?) -> Unit) {
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia(), onPicked)
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
        val pickedUrl = state.avatarUri?.toString()
        PictureSlot(
            imageUrl = pickedUrl ?: rememberMediaUrl(state.currentAvatarMediaId),
            name = state.name.ifBlank { "Community" },
            onPick = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
            busy = state.uploadingAvatar,
        )
        Text(
            text = if (state.uploadingAvatar) "Uploading picture…" else "Add a picture",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(top = UsTheme.spacing.m),
        )
    }
}

/** Name, handle, about, visibility. */
@Composable
private fun CommunityFields(state: CommunityFormUiState, viewModel: CommunityCreateViewModel) {
    ChatFormField(
        value = state.name,
        onValueChange = viewModel::onNameChange,
        label = "Name",
        placeholder = "What is this community called?",
        problem = state.nameProblem,
        counter = "${state.name.length}/${CommunityRules.NAME_MAX}",
        tag = "community_name",
    )
    ChatFormField(
        value = state.handle,
        onValueChange = viewModel::onHandleChange,
        label = "Handle",
        placeholder = "handle",
        leading = "@",
        problem = state.handleProblem,
        counter = if (state.isEdit) "The handle can't change." else "Lowercase letters, numbers and _ · 3–30",
        tag = "community_handle",
        trailing = {
            if (state.handleAvailable && !state.isEdit) {
                Icon(
                    imageVector = UsIcons.Check,
                    contentDescription = "Handle looks free",
                    tint = UsTheme.extended.statusSuccess,
                )
            }
        },
    )
    ChatFormField(
        value = state.description,
        onValueChange = viewModel::onDescriptionChange,
        label = "About",
        placeholder = "What members can expect here",
        problem = state.descriptionProblem,
        counter = "${state.description.length}/${CommunityRules.DESCRIPTION_MAX}",
        singleLine = false,
        minLines = ABOUT_LINES,
        tag = "community_about",
    )
    TwoWayChoice(
        label = "Who can find it",
        options = listOf(
            Community.VISIBILITY_PUBLIC to "Public",
            Community.VISIBILITY_PRIVATE to "Private",
        ),
        selected = state.visibility,
        onSelect = viewModel::onVisibilityChange,
    )
}

private const val ABOUT_LINES = 3
