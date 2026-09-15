package com.us.android.feature.dating.selfie

import android.content.Context
import android.graphics.Matrix
import android.graphics.SurfaceTexture
import android.media.MediaRecorder
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Size
import android.view.Surface
import android.view.TextureView
import androidx.camera.core.CameraSelector
import androidx.camera.core.Preview
import androidx.camera.core.SurfaceRequest
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import java.io.File
import kotlin.math.max

/**
 * The FRONT camera for the blink-twice clip, through CameraX core 1.4.1.
 *
 * Based on `feature/rider/.../selfie/SelfieCamera.kt` (features may not share
 * code): the preview is a [TextureView] fed by a [Preview.SurfaceProvider],
 * because camera-view is not in the offline cache. That module takes a photo;
 * this one records VIDEO, and camera-video is not in the cache either, so the
 * clip is written by the platform [MediaRecorder] from a surface: for the few
 * seconds of recording the Preview's surface is swapped from the TextureView to
 * the recorder's input surface, then swapped back. No audio source is set, so
 * no microphone permission is involved.
 *
 * UNVERIFIED ON A DEVICE (no device automation is allowed on the only handset):
 * the on-screen preview holds its last frame while recording, and the clip's
 * orientation comes from the sensor rotation as an MP4 orientation hint.
 */
@Composable
fun SelfieCameraPreview(
    onReady: (SelfieRecorder) -> Unit,
    onUnavailable: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val textureView = remember { TextureView(context) }
    val textureProvider = remember { TextureSurfaceProvider(textureView) }

    DisposableEffect(lifecycleOwner) {
        val future = ProcessCameraProvider.getInstance(context)
        var provider: ProcessCameraProvider? = null
        future.addListener(
            {
                val camera = runCatching { future.get() }.getOrNull()
                val hasFront = camera != null &&
                    runCatching { camera.hasCamera(CameraSelector.DEFAULT_FRONT_CAMERA) }.getOrDefault(false)
                if (camera == null || !hasFront) {
                    onUnavailable()
                    return@addListener
                }
                provider = camera
                val preview = Preview.Builder()
                    .setResolutionSelector(
                        ResolutionSelector.Builder()
                            .setResolutionStrategy(
                                ResolutionStrategy(Size(CLIP_LONG_EDGE, CLIP_SHORT_EDGE), ResolutionStrategy.FALLBACK_RULE_CLOSEST_LOWER_THEN_HIGHER),
                            )
                            .build(),
                    )
                    .build()
                preview.setSurfaceProvider(ContextCompat.getMainExecutor(context), textureProvider)
                val bound = runCatching {
                    camera.unbindAll()
                    camera.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_FRONT_CAMERA, preview)
                }.getOrNull()
                if (bound == null) {
                    onUnavailable()
                } else {
                    onReady(SelfieRecorder(context, preview, textureProvider, bound.cameraInfo.sensorRotationDegrees))
                }
            },
            ContextCompat.getMainExecutor(context),
        )
        onDispose { provider?.unbindAll() }
    }

    AndroidView(factory = { textureView }, modifier = modifier)
}

/**
 * Records one clip at a time into `cacheDir/dating-selfie` (emptied before each
 * clip; the uploader deletes the clip after upload). All calls on the main thread.
 */
class SelfieRecorder internal constructor(
    private val context: Context,
    private val preview: Preview,
    private val previewProvider: Preview.SurfaceProvider,
    private val rotationDegrees: Int,
) {
    private val main = ContextCompat.getMainExecutor(context)
    private val handler = Handler(Looper.getMainLooper())
    private var recording = false

    val isRecording: Boolean get() = recording

    /** Records for [durationMillis], then calls [onFinished] with the clip, or null when it failed. */
    fun record(durationMillis: Long, onFinished: (File?) -> Unit) {
        if (recording) return
        recording = true
        val directory = File(context.cacheDir, CLIP_DIR).apply {
            mkdirs()
            listFiles()?.forEach { it.delete() }
        }
        val file = File(directory, "selfie-${System.currentTimeMillis()}.mp4")
        preview.setSurfaceProvider(main) { request -> startRecording(request, file, durationMillis, onFinished) }
    }

    private fun startRecording(request: SurfaceRequest, file: File, durationMillis: Long, onFinished: (File?) -> Unit) {
        val recorder = newRecorder()
        val prepared = runCatching {
            recorder.setVideoSource(MediaRecorder.VideoSource.SURFACE)
            recorder.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            recorder.setVideoEncoder(MediaRecorder.VideoEncoder.H264)
            recorder.setVideoSize(request.resolution.width, request.resolution.height)
            recorder.setVideoFrameRate(FRAME_RATE)
            recorder.setVideoEncodingBitRate(BIT_RATE)
            recorder.setOrientationHint(rotationDegrees)
            recorder.setOutputFile(file.absolutePath)
            recorder.prepare()
        }.isSuccess
        if (!prepared) {
            request.willNotProvideSurface()
            recorder.release()
            finish(file, saved = false, onFinished)
            return
        }
        // The recorder is released only once the camera has stopped using its surface.
        request.provideSurface(recorder.surface, main) { recorder.release() }
        if (runCatching { recorder.start() }.isFailure) {
            finish(file, saved = false, onFinished)
            return
        }
        handler.postDelayed({
            val saved = runCatching { recorder.stop() }.isSuccess
            finish(file, saved, onFinished)
        }, durationMillis)
    }

    private fun finish(file: File, saved: Boolean, onFinished: (File?) -> Unit) {
        // Back to the on-screen preview; the camera lets go of the recorder's surface.
        preview.setSurfaceProvider(main, previewProvider)
        recording = false
        if (saved && file.length() > 0L) {
            onFinished(file)
        } else {
            file.delete()
            onFinished(null)
        }
    }

    @Suppress("DEPRECATION")
    private fun newRecorder(): MediaRecorder =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else MediaRecorder()

    private companion object {
        const val CLIP_DIR = "dating-selfie"
        const val FRAME_RATE = 30
        const val BIT_RATE = 2_500_000
    }
}

/**
 * Feeds a [TextureView] to CameraX: provides the Surface once both the request
 * and the SurfaceTexture exist, and keeps a centre-crop transform. Re-provides
 * after the recorder hands the stream back.
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

private const val CLIP_LONG_EDGE = 1280
private const val CLIP_SHORT_EDGE = 720
