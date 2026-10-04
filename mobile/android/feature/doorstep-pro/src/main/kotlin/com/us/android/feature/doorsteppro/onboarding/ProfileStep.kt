package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.PickedPhoto
import com.us.android.feature.doorsteppro.camera.rememberPhotoSource
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.ui.BottomAction
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.ShotPreview
import com.us.android.feature.doorsteppro.ui.UploadField
import com.us.android.feature.doorsteppro.ui.UploadUi
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ProfileStepUiState(
    val name: String = "",
    val hasPhoto: Boolean = false,
    val photo: UploadUi = UploadUi.Idle,
    val localPath: String? = null,
    val saving: Boolean = false,
    val saved: Boolean = false,
    val message: UsMessage? = null,
)

/** The "profile" step: display name and a profile photo (`PATCH /pro/me`); both are required by MissingSteps. */
@HiltViewModel
class ProfileStepViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val uploads: PhotoUploads,
) : ViewModel() {

    private val _state = MutableStateFlow(ProfileStepUiState())
    val state: StateFlow<ProfileStepUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            (repository.me() as? ProResult.Success)?.value?.let { me ->
                _state.update { it.copy(name = me.displayName, hasPhoto = me.photoMediaId != null) }
            }
        }
    }

    fun onName(value: String) = _state.update { it.copy(name = value.take(MAX_NAME)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun onPhoto(photo: PickedPhoto) {
        _state.update { it.copy(photo = UploadUi.Uploading(0f), localPath = photo.path) }
        viewModelScope.launch {
            when (val outcome = uploads.uploadImage(photo.uri) { p -> _state.update { it.copy(photo = UploadUi.Uploading(p)) } }) {
                is UploadOutcome.Ready -> _state.update { it.copy(photo = UploadUi.Uploaded(outcome.mediaId)) }
                is UploadOutcome.Failed -> _state.update { it.copy(photo = UploadUi.Failed(outcome.message)) }
            }
        }
    }

    fun save() {
        val current = _state.value
        val name = current.name.trim()
        val mediaId = (current.photo as? UploadUi.Uploaded)?.mediaId
        when {
            name.length < MIN_NAME -> _state.update { it.copy(message = errorMessage("Enter your name.")) }
            mediaId == null && !current.hasPhoto -> _state.update { it.copy(message = errorMessage("Add a clear photo of your face.")) }
            current.saving -> Unit
            else -> {
                _state.update { it.copy(saving = true) }
                viewModelScope.launch {
                    when (val result = repository.updateProfile(displayName = name, photoMediaId = mediaId)) {
                        is ProResult.Success -> _state.update { it.copy(saving = false, saved = true) }
                        is ProResult.Failure -> _state.update { it.copy(saving = false, message = result.error.asMessage()) }
                    }
                }
            }
        }
    }

    private companion object {
        const val MIN_NAME = 2
        const val MAX_NAME = 80
    }
}

@Composable
fun ProfileStepScreen(onBack: () -> Unit, viewModel: ProfileStepViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.saved) { if (state.saved) onBack() }
    val source = rememberPhotoSource(onPicked = viewModel::onPhoto)
    ProScreen(
        title = "Your name and photo",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Save", onClick = viewModel::save, loading = state.saving, enabled = state.photo !is UploadUi.Uploading) },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            ProCard {
                CardHeading("How customers see you", "Your first name and photo appear on the customer's booking once you accept.")
                UsTextField(value = state.name, onValueChange = viewModel::onName, label = "Your name")
                UploadField(
                    label = if (state.hasPhoto) "Profile photo (saved)" else "Profile photo",
                    state = state.photo,
                    onPick = source.pickFromGallery,
                    hint = if (state.hasPhoto) "Tap to replace" else "A clear photo of your face",
                )
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    UsSecondaryButton(text = "Take a photo", onClick = source.takePhoto, modifier = Modifier.weight(1f))
                }
                state.localPath?.let { ShotPreview(it, Modifier.fillMaxWidth().aspectRatio(1f)) }
                InfoNote("Face the camera in good light, no cap or sunglasses.")
            }
        }
    }
}
