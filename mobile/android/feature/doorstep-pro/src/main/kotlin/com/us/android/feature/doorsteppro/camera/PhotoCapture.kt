package com.us.android.feature.doorsteppro.camera

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat

/** A photo ready to upload: its content:// URI, and the local file when this app captured it (for the preview). */
data class PickedPhoto(val uri: String, val path: String?)

/** Two ways to get a photo: the system photo picker, or the camera. */
class PhotoSource internal constructor(
    val pickFromGallery: () -> Unit,
    val takePhoto: () -> Unit,
)

/**
 * The photo picker (no storage permission: the system picker grants just the
 * chosen image) and the system camera writing into this app's cache through
 * [ProPhotoFileProvider]. The app declares CAMERA for the selfie, and a
 * declared-but-denied CAMERA makes the camera intent fail, so the camera path
 * asks for it first; [onCameraDenied] tells the screen when it was refused.
 */
@Composable
fun rememberPhotoSource(onPicked: (PickedPhoto) -> Unit, onCameraDenied: () -> Unit = {}): PhotoSource {
    val context = LocalContext.current
    var pendingPath by rememberSaveable { mutableStateOf<String?>(null) }
    var pendingUri by rememberSaveable { mutableStateOf<String?>(null) }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        uri?.let { onPicked(PickedPhoto(uri = it.toString(), path = null)) }
    }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { saved ->
        val uri = pendingUri
        if (saved && uri != null) onPicked(PickedPhoto(uri = uri, path = pendingPath))
        pendingUri = null
        pendingPath = null
    }
    fun launchCamera() {
        val (file, uri) = JobPhotoFiles.newTarget(context)
        pendingPath = file.absolutePath
        pendingUri = uri.toString()
        runCatching { camera.launch(uri) }.onFailure { onCameraDenied() }
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) launchCamera() else onCameraDenied()
    }
    return remember(picker, camera, permission) {
        PhotoSource(
            pickFromGallery = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
            takePhoto = {
                if (context.cameraGranted()) launchCamera() else permission.launch(Manifest.permission.CAMERA)
            },
        )
    }
}

fun Context.cameraGranted(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED
