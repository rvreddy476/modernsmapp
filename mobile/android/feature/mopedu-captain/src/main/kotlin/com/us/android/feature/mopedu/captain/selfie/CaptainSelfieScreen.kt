package com.us.android.feature.mopedu.captain.selfie

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.media.ExifInterface
import android.net.Uri
import android.provider.Settings
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
import com.us.android.feature.mopedu.captain.ui.CaptainCard
import com.us.android.feature.mopedu.captain.ui.CaptainScreen
import com.us.android.feature.mopedu.captain.ui.CardHeading
import com.us.android.feature.mopedu.captain.ui.InfoNote
import com.us.android.feature.mopedu.captain.ui.MessagePane
import com.us.android.feature.mopedu.captain.ui.StatusBadge
import kotlinx.coroutines.launch

/**
 * The onboarding selfie: front camera only, one retake, then the upload and
 * the document record. [onDone] fires once the record is in (the caller
 * reloads the documents step); [onBack] leaves without one.
 *
 * Copied from :feature:rider's selfie/SelfieScreen.kt on the captain's scaffold.
 */
@Composable
fun CaptainSelfieScreen(onDone: () -> Unit, onBack: () -> Unit, viewModel: CaptainSelfieViewModel = hiltViewModel()) {
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

    CaptainScreen(title = "Your selfie", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(padding)
                .padding(vertical = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            when (val step = state.step) {
                SelfieStep.Camera -> when {
                    state.cameraUnavailable -> MessagePane(
                        title = "No front camera available",
                        body = "Mopedu takes your selfie with the front camera, and this phone doesn't offer one right now.",
                    )
                    !cameraGranted -> CaptainCard {
                        CardHeading(
                            "Take a selfie with your front camera",
                            "Mopedu matches it to the photo on your driving licence to confirm it's you. The camera is used only on this screen.",
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
                        InfoNote(POSE_HINT)
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
                    if (state.canRetake) {
                        UsSecondaryButton(text = "Retake (once)", onClick = viewModel::retake, modifier = Modifier.fillMaxWidth())
                    } else {
                        InfoNote("That was your retake. This photo is the one that goes in.")
                    }
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
                is SelfieStep.Submitted -> CaptainCard {
                    CardHeading("Selfie submitted", "Mopedu compares it with your licence photo automatically.")
                    StatusBadge(step.status)
                    UsButton(text = "Done", onClick = onDone, modifier = Modifier.fillMaxWidth())
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

private fun Context.openAppSettings() {
    startActivity(
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", packageName, null)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
    )
}

private const val POSE_HINT =
    "Hold the phone at eye level and look straight into the camera, in good light, with no cap, mask or sunglasses. Only the front camera is used."
private const val PREVIEW_ASPECT = 3f / 4f
private const val PREVIEW_SAMPLE = 4
