package com.us.android.feature.chat.ui

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageCapture
import androidx.camera.core.ImageCaptureException
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.chat.ui.home.pressScale
import java.io.File

/**
 * The composer's camera (CONTRACT §9): a full-window CameraX preview with a
 * shutter, the flip and close. The photo is written to the app's cache and
 * handed back as a file Uri, which then STAGES on the composer exactly like
 * a gallery pick — nothing is sent until Send.
 */
@Composable
internal fun ChatCameraDialog(onCaptured: (Uri) -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    var granted by remember {
        mutableStateOf(
            ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED,
        )
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        granted = ok
        if (!ok) onDismiss()
    }
    LaunchedEffect(Unit) { if (!granted) permission.launch(Manifest.permission.CAMERA) }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(UsTheme.extended.stage)
                .testTag("chat-camera"),
        ) {
            if (granted) CameraBody(onCaptured = onCaptured, onDismiss = onDismiss)
        }
    }
}

@Composable
private fun CameraBody(onCaptured: (Uri) -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current
    var back by remember { mutableStateOf(true) }
    var busy by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    val capture = remember { ImageCapture.Builder().setCaptureMode(ImageCapture.CAPTURE_MODE_MINIMIZE_LATENCY).build() }
    val previewView = remember { PreviewView(context) }
    DisposableEffect(back) {
        val future = ProcessCameraProvider.getInstance(context)
        val executor = ContextCompat.getMainExecutor(context)
        future.addListener(
            {
                runCatching {
                    val provider = future.get()
                    val preview = Preview.Builder().build().also { it.surfaceProvider = previewView.surfaceProvider }
                    provider.unbindAll()
                    provider.bindToLifecycle(
                        lifecycle,
                        if (back) CameraSelector.DEFAULT_BACK_CAMERA else CameraSelector.DEFAULT_FRONT_CAMERA,
                        preview,
                        capture,
                    )
                }.onFailure { failed = true }
            },
            executor,
        )
        onDispose { runCatching { future.get().unbindAll() } }
    }
    Box(modifier = Modifier.fillMaxSize()) {
        AndroidView(factory = { previewView }, modifier = Modifier.fillMaxSize())
        if (failed) {
            Text(
                "The camera couldn't start.",
                color = UsTheme.extended.onMedia,
                modifier = Modifier.align(Alignment.Center),
            )
        }
        IconButton(
            onClick = onDismiss,
            modifier = Modifier
                .align(Alignment.TopStart)
                .statusBarsPadding()
                .padding(UsTheme.spacing.m)
                .testTag("chat-camera-close"),
        ) {
            Icon(UsIcons.Close, contentDescription = "Close camera", tint = UsTheme.extended.onMedia)
        }
        IconButton(
            onClick = { back = !back },
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .navigationBarsPadding()
                .padding(UsTheme.spacing.xl),
        ) {
            Icon(UsIcons.RotateCw, contentDescription = "Switch camera", tint = UsTheme.extended.onMedia)
        }
        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .navigationBarsPadding()
                .padding(bottom = UsTheme.spacing.xl)
                .size(SHUTTER)
                .border(SHUTTER_RING, UsTheme.extended.onMedia, CircleShape)
                .padding(SHUTTER_GAP)
                .background(if (busy) UsTheme.extended.onMediaMuted else UsTheme.extended.onMedia, CircleShape)
                .pressScale(enabled = !busy && !failed, onClick = {
                    busy = true
                    val file = File(context.cacheDir, "camera-${System.currentTimeMillis()}.jpg")
                    capture.takePicture(
                        ImageCapture.OutputFileOptions.Builder(file).build(),
                        ContextCompat.getMainExecutor(context),
                        object : ImageCapture.OnImageSavedCallback {
                            override fun onImageSaved(output: ImageCapture.OutputFileResults) {
                                busy = false
                                onCaptured(Uri.fromFile(file))
                            }

                            override fun onError(exception: ImageCaptureException) {
                                busy = false
                                file.delete()
                            }
                        },
                    )
                })
                .semantics { contentDescription = "Take photo" }
                .testTag("chat-camera-shutter"),
        )
    }
}

private val SHUTTER = 76.dp
private val SHUTTER_RING = 4.dp
private val SHUTTER_GAP = 6.dp
