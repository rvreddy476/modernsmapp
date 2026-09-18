package com.us.android.feature.mopedu.captain.selfie

import android.content.Context
import android.graphics.Matrix
import android.graphics.SurfaceTexture
import android.net.Uri
import android.util.Size
import android.view.Surface
import android.view.TextureView
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageCapture
import androidx.camera.core.ImageCaptureException
import androidx.camera.core.Preview
import androidx.camera.core.SurfaceRequest
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.us.android.feature.mopedu.captain.R
import kotlinx.coroutines.suspendCancellableCoroutine
import java.io.File
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.math.max

/** Shares only cacheDir/selfies (res/xml/captain_selfie_paths.xml). A subclass so its manifest key is unique. */
class CaptainSelfieFileProvider : FileProvider(R.xml.captain_selfie_paths)

/** A captured selfie: the file (for its EXIF orientation) and its content:// URI (for the upload). */
data class SelfieShot(val path: String, val uri: String)

/**
 * The FRONT camera only, through CameraX core (1.4.1) — no camera-view.
 *
 * COPIED from :feature:rider/selfie/SelfieCamera.kt (2026-09-18); the two
 * differ only in package and FileProvider authority. Lift when a third copy
 * appears.
 *
 * `androidx.camera:camera-view` (PreviewView) is not in the offline cache, so
 * the preview is a [TextureView] fed by a [Preview.SurfaceProvider] that sizes
 * the SurfaceTexture to the camera's resolution and scales it to fill the view.
 * The camera pipeline applies the sensor rotation to a SurfaceTexture, so for
 * this portrait screen only the aspect needs correcting. UNVERIFIED ON A
 * DEVICE: a preview that looks rotated or stretched on some handset is
 * cosmetic — the captured JPEG carries correct EXIF orientation either way.
 * Swap for PreviewView when camera-view reaches the cache.
 *
 * [onUnavailable] fires when the device has no front camera or CameraX cannot
 * bind; the screen then offers nothing it cannot honour.
 */
@Composable
fun FrontCameraPreview(
    onReady: (ImageCapture) -> Unit,
    onUnavailable: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val textureView = remember { TextureView(context) }
    val surfaceProvider = remember { TextureSurfaceProvider(textureView) }

    DisposableEffect(lifecycleOwner) {
        val future = ProcessCameraProvider.getInstance(context)
        var provider: ProcessCameraProvider? = null
        future.addListener(
            {
                val camera = runCatching { future.get() }.getOrNull()
                val hasFront = camera != null && runCatching { camera.hasCamera(CameraSelector.DEFAULT_FRONT_CAMERA) }.getOrDefault(false)
                if (camera == null || !hasFront) {
                    onUnavailable()
                    return@addListener
                }
                provider = camera
                val preview = Preview.Builder().build().also { it.setSurfaceProvider(ContextCompat.getMainExecutor(context), surfaceProvider) }
                val capture = ImageCapture.Builder()
                    .setCaptureMode(ImageCapture.CAPTURE_MODE_MINIMIZE_LATENCY)
                    .build()
                val bound = runCatching {
                    camera.unbindAll()
                    camera.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_FRONT_CAMERA, preview, capture)
                }.isSuccess
                if (bound) onReady(capture) else onUnavailable()
            },
            ContextCompat.getMainExecutor(context),
        )
        onDispose { provider?.unbindAll() }
    }

    AndroidView(factory = { textureView }, modifier = modifier)
}

/** Takes the selfie into cacheDir/selfies and returns it with its FileProvider URI. */
suspend fun ImageCapture.takeSelfie(context: Context): SelfieShot = suspendCancellableCoroutine { continuation ->
    val directory = File(context.cacheDir, SELFIE_DIR).apply {
        mkdirs()
        listFiles()?.forEach { it.delete() } // only the newest selfie is ever kept
    }
    val file = File(directory, "selfie-${System.currentTimeMillis()}.jpg")
    takePicture(
        ImageCapture.OutputFileOptions.Builder(file).build(),
        ContextCompat.getMainExecutor(context),
        object : ImageCapture.OnImageSavedCallback {
            override fun onImageSaved(outputFileResults: ImageCapture.OutputFileResults) {
                val uri: Uri = FileProvider.getUriForFile(context, "${context.packageName}.captain.selfie", file)
                continuation.resume(SelfieShot(file.absolutePath, uri.toString()))
            }

            override fun onError(exception: ImageCaptureException) {
                continuation.resumeWithException(exception)
            }
        },
    )
}

/**
 * Feeds a [TextureView] to CameraX: provides the Surface once both the request
 * and the SurfaceTexture exist, and keeps a centre-crop transform.
 */
private class TextureSurfaceProvider(private val view: TextureView) :
    Preview.SurfaceProvider,
    TextureView.SurfaceTextureListener {

    private var pending: SurfaceRequest? = null
    private var resolution: Size? = null
    private var rotationDegrees = 0

    init {
        view.surfaceTextureListener = this
        view.addOnLayoutChangeListener { _, _, _, _, _, _, _, _, _ -> updateTransform() }
    }

    override fun onSurfaceRequested(request: SurfaceRequest) {
        pending?.willNotProvideSurface()
        pending = request
        resolution = request.resolution
        request.setTransformationInfoListener(ContextCompat.getMainExecutor(view.context)) { info ->
            rotationDegrees = info.rotationDegrees
            updateTransform()
        }
        view.surfaceTexture?.let(::provide)
    }

    private fun provide(texture: SurfaceTexture) {
        val request = pending ?: return
        pending = null
        texture.setDefaultBufferSize(request.resolution.width, request.resolution.height)
        val surface = Surface(texture)
        request.provideSurface(surface, ContextCompat.getMainExecutor(view.context)) { surface.release() }
        updateTransform()
    }

    override fun onSurfaceTextureAvailable(surface: SurfaceTexture, width: Int, height: Int) = provide(surface)

    override fun onSurfaceTextureSizeChanged(surface: SurfaceTexture, width: Int, height: Int) = updateTransform()

    override fun onSurfaceTextureDestroyed(surface: SurfaceTexture): Boolean = true

    override fun onSurfaceTextureUpdated(surface: SurfaceTexture) = Unit

    private fun updateTransform() {
        val size = resolution ?: return
        val viewWidth = view.width.toFloat()
        val viewHeight = view.height.toFloat()
        if (viewWidth == 0f || viewHeight == 0f) return
        val sideways = rotationDegrees % HALF_TURN != 0
        val contentWidth = if (sideways) size.height.toFloat() else size.width.toFloat()
        val contentHeight = if (sideways) size.width.toFloat() else size.height.toFloat()
        val scale = max(viewWidth / contentWidth, viewHeight / contentHeight)
        view.setTransform(
            Matrix().apply {
                setScale(contentWidth * scale / viewWidth, contentHeight * scale / viewHeight, viewWidth / 2f, viewHeight / 2f)
            },
        )
    }

    private companion object {
        const val HALF_TURN = 180
    }
}

private const val SELFIE_DIR = "selfies"
