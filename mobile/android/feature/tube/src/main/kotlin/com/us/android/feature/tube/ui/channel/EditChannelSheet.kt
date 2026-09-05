package com.us.android.feature.tube.ui.channel

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelAbout
import com.us.android.core.feed.data.ChannelCreateError
import com.us.android.core.feed.data.ChannelHandle
import com.us.android.core.feed.data.ChannelName
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.model.Channel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class EditChannelUiState(
    val name: String = "",
    val handle: String = "",
    val about: String = "",
    val saving: Boolean = false,
    val nameError: String? = null,
    val handleError: String? = null,
    val aboutError: String? = null,
    val error: String? = null,
    val saved: Boolean = false,
    val seeded: Boolean = false,
) {
    val canSave: Boolean
        get() = !saving && ChannelName.problem(name) == null && ChannelHandle.isValid(handle) &&
            ChannelAbout.problem(about) == null
}

/** "Edit channel": name, handle and About through `PATCH v1/channels/me`. The cached channel updates on save. */
@HiltViewModel
class EditChannelViewModel @Inject constructor(private val channels: ChannelRepository) : ViewModel() {

    private val _state = MutableStateFlow(EditChannelUiState())
    val state: StateFlow<EditChannelUiState> = _state.asStateFlow()

    /** The sheet seeds the fields from the channel once; a rotation keeps the edits. */
    fun seed(channel: Channel) = _state.update {
        if (it.seeded) {
            it
        } else {
            it.copy(name = channel.name, handle = channel.handle, about = channel.about, seeded = true)
        }
    }

    fun onNameChanged(value: String) = _state.update {
        it.copy(name = value.take(ChannelName.MAX_LENGTH), nameError = null)
    }

    fun onHandleChanged(value: String) = _state.update {
        it.copy(handle = ChannelHandle.normalize(value), handleError = null)
    }

    fun onAboutChanged(value: String) = _state.update {
        it.copy(about = value.take(ChannelAbout.MAX_LENGTH), aboutError = null)
    }

    fun save() {
        val current = _state.value
        if (!current.canSave) return
        _state.update { it.copy(saving = true, error = null) }
        viewModelScope.launch {
            when (val result = channels.update(current.name, current.handle, current.about)) {
                is AppResult.Success -> _state.update { it.copy(saving = false, saved = true) }
                is AppResult.Failure -> _state.update { it.refused(ChannelRepository.createError(result.error)) }
            }
        }
    }

    private fun EditChannelUiState.refused(error: ChannelCreateError): EditChannelUiState = when (error) {
        ChannelCreateError.HandleTaken -> copy(saving = false, handleError = "That handle is taken.")
        is ChannelCreateError.InvalidHandle -> copy(saving = false, handleError = error.message)
        is ChannelCreateError.InvalidName -> copy(saving = false, nameError = error.message)
        is ChannelCreateError.InvalidAbout -> copy(saving = false, aboutError = error.message)
        ChannelCreateError.ChannelExists -> copy(saving = false, error = "That didn't go through. Try again.")
        is ChannelCreateError.Other -> copy(saving = false, error = error.message)
    }
}

/** The You page's "Edit channel" sheet: three fields and Save. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EditChannelSheet(
    channel: Channel,
    onDismiss: () -> Unit,
    viewModel: EditChannelViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(channel.userId) { viewModel.seed(channel) }
    if (state.saved) {
        LaunchedEffect(Unit) { onDismiss() }
    }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        modifier = Modifier.testTag("edit_channel_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding()
                .imePadding(),
        ) {
            Text(
                text = "Edit channel",
                style = MaterialTheme.typography.titleLarge.copy(fontSize = TITLE_SIZE),
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            EditChannelFields(state = state, viewModel = viewModel)
            state.error?.let { message ->
                Spacer(Modifier.height(UsTheme.spacing.l))
                Text(
                    text = message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))
            UsButton(
                text = "Save",
                onClick = viewModel::save,
                enabled = state.canSave,
                loading = state.saving,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("edit_channel_save"),
            )
        }
    }
}

/** The three fields, each with its own error line. */
@Composable
private fun EditChannelFields(state: EditChannelUiState, viewModel: EditChannelViewModel) {
    UsTextField(
        value = state.name,
        onValueChange = viewModel::onNameChanged,
        label = "Channel name",
        errorText = state.nameError,
        enabled = !state.saving,
        modifier = Modifier.testTag("edit_channel_name"),
    )
    Spacer(Modifier.height(UsTheme.spacing.l))
    UsTextField(
        value = state.handle,
        onValueChange = viewModel::onHandleChanged,
        label = "Handle",
        placeholder = "yourhandle",
        errorText = state.handleError ?: ChannelHandle.problem(state.handle),
        enabled = !state.saving,
        modifier = Modifier.testTag("edit_channel_handle"),
    )
    Spacer(Modifier.height(UsTheme.spacing.l))
    UsTextField(
        value = state.about,
        onValueChange = viewModel::onAboutChanged,
        label = "About",
        errorText = state.aboutError,
        enabled = !state.saving,
        singleLine = false,
        modifier = Modifier.testTag("edit_channel_about"),
    )
}

private const val SCRIM_ALPHA = 0.55f
private val SHEET_RADIUS = 28.dp
private val TITLE_SIZE = 20.sp
