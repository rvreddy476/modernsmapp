package com.us.android.feature.doorsteppro.onboarding

import android.Manifest
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.ImageCapture
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.camera.FrontCameraPreview
import com.us.android.feature.doorsteppro.camera.SelfieShot
import com.us.android.feature.doorsteppro.camera.cameraGranted
import com.us.android.feature.doorsteppro.camera.takeSelfie
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.MessagePane
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.ShotPreview
import com.us.android.feature.doorsteppro.ui.Tone
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

sealed interface SelfieStep {
    data object Camera : SelfieStep

    data class Review(val shot: SelfieShot) : SelfieStep

    data class Uploading(val shot: SelfieShot, val progress: Float) : SelfieStep

    /** passed: matched. pending: below the threshold, a person reviews it. */
    data class Result(val passed: Boolean) : SelfieStep
}

data class SelfieUiState(
    val step: SelfieStep = SelfieStep.Camera,
    val cameraUnavailable: Boolean = false,
    /** 422 DOORSTEP_ONBOARDING_INCOMPLETE: Aadhaar must come first. */
    val needsAadhaar: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * The selfie face match: FRONT camera only (CameraX) → upload → `POST
 * /pro/selfie {media_id}`. The server compares it with the DigiLocker photo:
 * passed, pending (below the threshold, reviewed by a person) or 422
 * DOORSTEP_FACE_MATCH_FAILED (retake).
 */
@HiltViewModel
class SelfieViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val uploads: PhotoUploads,
) : ViewModel() {

    private val _state = MutableStateFlow(SelfieUiState())
    val state: StateFlow<SelfieUiState> = _state.asStateFlow()

    fun onCaptured(shot: SelfieShot) = _state.update { it.copy(step = SelfieStep.Review(shot)) }

    fun onCaptureFailed() = _state.update { it.copy(message = errorMessage("The photo didn't take. Try again.")) }

    fun onCameraUnavailable() = _state.update { it.copy(cameraUnavailable = true) }

    fun retake() = _state.update { it.copy(step = SelfieStep.Camera) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val shot = (_state.value.step as? SelfieStep.Review)?.shot ?: return
        _state.update { it.copy(step = SelfieStep.Uploading(shot, 0f)) }
        viewModelScope.launch {
            val mediaId = when (val outcome = uploads.uploadImage(shot.uri) { p -> _state.update { it.copy(step = SelfieStep.Uploading(shot, p)) } }) {
                is UploadOutcome.Ready -> outcome.mediaId
                is UploadOutcome.Failed -> {
                    _state.update { it.copy(step = SelfieStep.Review(shot), message = errorMessage(outcome.message)) }
                    return@launch
                }
            }
            when (val result = repository.submitSelfie(mediaId)) {
                is ProResult.Success -> _state.update { it.copy(step = SelfieStep.Result(passed = result.value.status == PASSED)) }
                is ProResult.Failure -> _state.update {
                    it.copy(
                        step = SelfieStep.Camera,
                        needsAadhaar = result.error.code == ProCodes.ONBOARDING_INCOMPLETE,
                        message = result.error.asMessage(),
                    )
                }
            }
        }
    }

    private companion object {
        const val PASSED = "passed"
    }
}

@Composable
fun SelfieStepScreen(onBack: () -> Unit, viewModel: SelfieViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var cameraGranted by remember { mutableStateOf(context.cameraGranted()) }
    var askedOnce by remember { mutableStateOf(false) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        cameraGranted = granted
        askedOnce = true
    }
    var capture by remember { mutableStateOf<ImageCapture?>(null) }

    ProScreen(title = "Selfie", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            when (val step = state.step) {
                SelfieStep.Camera -> when {
                    state.needsAadhaar -> MessagePane(
                        title = "Verify Aadhaar first",
                        body = "Your selfie is matched with your Aadhaar photo, so DigiLocker comes first.",
                        primaryLabel = "Back to checklist",
                        onPrimary = onBack,
                    )
                    state.cameraUnavailable -> MessagePane(
                        title = "No front camera available",
                        body = "Doorstep Pro takes your selfie with the front camera, and this phone doesn't offer one right now.",
                        primaryLabel = "Back",
                        onPrimary = onBack,
                    )
                    !cameraGranted -> ProCard {
                        CardHeading(
                            "Take a selfie with your front camera",
                            "Doorstep matches it with your Aadhaar photo to confirm it's you. The camera is used only on this screen.",
                        )
                        UsButton(
                            text = if (askedOnce) "Allow camera in Settings" else "Allow camera",
                            onClick = { permission.launch(Manifest.permission.CAMERA) },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                    else -> {
                        InfoNote("Face the screen in good light, no cap or sunglasses. Only the front camera is used.")
                        FrontCameraPreview(
                            onReady = { capture = it },
                            onUnavailable = viewModel::onCameraUnavailable,
                            modifier = Modifier.fillMaxWidth().aspectRatio(PREVIEW_ASPECT).clip(RoundedCornerShape(UsTheme.radii.large)),
                        )
                        UsButton(
                            text = "Take selfie",
                            enabled = capture != null,
                            onClick = {
                                val camera = capture ?: return@UsButton
                                scope.launch {
                                    runCatching { camera.takeSelfie(context) }
                                        .onSuccess(viewModel::onCaptured)
                                        .onFailure { viewModel.onCaptureFailed() }
                                }
                            },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
                is SelfieStep.Review -> {
                    ShotPreview(step.shot.path, Modifier.fillMaxWidth().aspectRatio(PREVIEW_ASPECT), description = "Your selfie")
                    UsButton(text = "Use this photo", onClick = viewModel::submit, modifier = Modifier.fillMaxWidth())
                    UsSecondaryButton(text = "Retake", onClick = viewModel::retake, modifier = Modifier.fillMaxWidth())
                }
                is SelfieStep.Uploading -> {
                    ShotPreview(step.shot.path, Modifier.fillMaxWidth().aspectRatio(PREVIEW_ASPECT), description = "Your selfie")
                    LinearProgressIndicator(
                        progress = { step.progress },
                        modifier = Modifier.fillMaxWidth(),
                        color = UsTheme.extended.accentSolid,
                        trackColor = UsTheme.extended.borderSubtle,
                    )
                }
                is SelfieStep.Result -> ProCard {
                    if (step.passed) {
                        CardHeading("It's a match", "Your selfie matches your Aadhaar photo.")
                        Pill("Verified", Tone.Positive)
                    } else {
                        CardHeading("Sent for a quick check", "The match wasn't certain, so a person at Doorstep will look at it.")
                        Pill("In review", Tone.Warning)
                    }
                    UsButton(text = "Done", onClick = onBack, modifier = Modifier.fillMaxWidth())
                }
            }
        }
    }
}

private const val PREVIEW_ASPECT = 3f / 4f
