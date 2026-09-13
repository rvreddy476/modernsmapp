package com.us.android.feature.rider.selfie

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.media.ExifInterface
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.ImageCapture
import androidx.compose.foundation.Image
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
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.home.openAppSettings
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.DocumentStatusText
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.MessagePane
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderPill
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.contentPadding
import kotlinx.coroutines.launch

@Composable
fun SelfieScreen(onBack: () -> Unit, viewModel: SelfieViewModel = hiltViewModel()) {
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

    RiderScreen(title = "Selfie", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(contentPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            when (val step = state.step) {
                SelfieStep.Camera -> when {
                    state.cameraUnavailable -> MessagePane(
                        title = "No front camera available",
                        body = "Feast Rider takes your selfie with the front camera, and this phone doesn't offer one right now.",
                        primaryLabel = "Back",
                        onPrimary = onBack,
                    )
                    !cameraGranted -> RiderCard {
                        CardHeading(
                            "Take a selfie with your front camera",
                            "Feast compares it with your documents to confirm it's you. The camera is used only on this screen.",
                        )
                        if (askedOnce) {
                            UsButton(text = "Open settings", onClick = { context.openAppSettings() }, modifier = Modifier.fillMaxWidth())
                        } else {
                            UsButton(
                                text = "Allow camera",
                                onClick = { permission.launch(Manifest.permission.CAMERA) },
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                    }
                    else -> {
                        InfoNote("Face the screen in good light, no cap or sunglasses. Only the front camera is used.")
                        FrontCameraPreview(
                            onReady = { capture = it },
                            onUnavailable = viewModel::onCameraUnavailable,
                            modifier = Modifier
                                .fillMaxWidth()
                                .aspectRatio(PREVIEW_ASPECT)
                                .clip(RoundedCornerShape(UsTheme.radii.large)),
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
                    ShotPreview(step.shot)
                    UsButton(text = "Use this photo", onClick = viewModel::submit, modifier = Modifier.fillMaxWidth())
                    UsSecondaryButton(text = "Retake", onClick = viewModel::retake, modifier = Modifier.fillMaxWidth())
                }
                is SelfieStep.Uploading -> {
                    ShotPreview(step.shot)
                    LinearProgressIndicator(
                        progress = { step.progress },
                        modifier = Modifier.fillMaxWidth(),
                        color = UsTheme.extended.accentSolid,
                        trackColor = UsTheme.extended.borderSubtle,
                    )
                }
                is SelfieStep.Submitted -> RiderCard {
                    CardHeading("Selfie submitted", "Feast reviews it with your documents.")
                    RiderPill(DocumentStatusText.label(step.status), DocumentStatusText.tone(step.status))
                    UsButton(text = "Done", onClick = onBack, modifier = Modifier.fillMaxWidth())
                }
            }
        }
    }
}

@Composable
private fun ShotPreview(shot: SelfieShot) {
    val bitmap = remember(shot.path) { decodeUpright(shot.path) }
    if (bitmap != null) {
        Image(
            bitmap = bitmap.asImageBitmap(),
            contentDescription = "Your selfie",
            contentScale = ContentScale.Crop,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(PREVIEW_ASPECT)
                .clip(RoundedCornerShape(UsTheme.radii.large)),
        )
    }
}

/** A small, upright copy for display only; the upload sends the original file. */
private fun decodeUpright(path: String): Bitmap? = runCatching {
    val bitmap = BitmapFactory.decodeFile(path, BitmapFactory.Options().apply { inSampleSize = PREVIEW_SAMPLE }) ?: return null
    val degrees = when (ExifInterface(path).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)) {
        ExifInterface.ORIENTATION_ROTATE_90 -> 90f
        ExifInterface.ORIENTATION_ROTATE_180 -> 180f
        ExifInterface.ORIENTATION_ROTATE_270 -> 270f
        else -> 0f
    }
    if (degrees == 0f) bitmap else Bitmap.createBitmap(bitmap, 0, 0, bitmap.width, bitmap.height, Matrix().apply { postRotate(degrees) }, true)
}.getOrNull()

private fun Context.cameraGranted(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

private const val PREVIEW_ASPECT = 3f / 4f
private const val PREVIEW_SAMPLE = 4
