package com.us.android.feature.dating.selfie

import android.content.Context
import android.graphics.Matrix
import android.graphics.SurfaceTexture
import android.os.Handler
import android.os.Looper
import android.util.Size
import android.view.Surface
import android.view.TextureView
import android.view.View
import androidx.camera.core.CameraSelector
import androidx.camera.core.Preview
import androidx.camera.core.SurfaceRequest
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.video.FallbackStrategy
import androidx.camera.video.FileOutputOptions
import androidx.camera.video.Quality
import androidx.camera.video.QualitySelector
import androidx.camera.video.Recorder
import androidx.camera.video.Recording
import androidx.camera.video.VideoCapture
import androidx.camera.video.VideoRecordEvent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import java.io.File
import kotlin.math.max

/**
 * The FRONT camera for the blink-twice clip, through CameraX 1.4.1.
 *
 * Two use cases are bound together: a [Preview] that draws into a [TextureView]
 * (camera-view is not in the offline cache, so the surface is provided by hand,
 * as in `feature/rider/.../selfie/SelfieCamera.kt` — features may not share
 * code), and a [VideoCapture] backed by a [Recorder] that writes the clip.
 * Because they are separate use cases the preview keeps running for the whole
 * recording, and the clip carries CameraX's target rotation, so what is
 * uploaded is upright rather than at the sensor's angle.
 *
 * No audio: [androidx.camera.video.PendingRecording.withAudioEnabled] is never
 * called, so no microphone permission is involved and nothing is heard.
 *
 * UNVERIFIED ON A DEVICE (no device automation is allowed on the only handset).
 */
@Composable
fun SelfieCameraPreview(
    onReady: (SelfieRecorder) -> Unit,
    onUnavailable: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val view = LocalView.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val textureView = remember { TextureView(context) }
    val textureProvider = remember { TextureSurfaceProvider(textureView) }

    DisposableEffect(lifecycleOwner) {
        val future = ProcessCameraProvider.getInstance(context)
        var provider: ProcessCameraProvider? = null
        var recorder: SelfieRecorder? = null
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
                    .setResolutionSelector(previewResolution())
                    .build()
                preview.setSurfaceProvider(ContextCompat.getMainExecutor(context), textureProvider)
                val videoCapture = VideoCapture.withOutput(
                    Recorder.Builder()
                        .setQualitySelector(clipQuality())
                        .setTargetVideoEncodingBitRate(BIT_RATE)
                        .build(),
                )
                videoCapture.targetRotation = displayRotation(view)
                val bound = runCatching {
                    camera.unbindAll()
                    camera.bindToLifecycle(
                        lifecycleOwner,
                        CameraSelector.DEFAULT_FRONT_CAMERA,
                        preview,
                        videoCapture,
                    )
                }.getOrNull()
                if (bound == null) {
                    onUnavailable()
                } else {
                    val made = SelfieRecorder(context, videoCapture, rotation = { displayRotation(view) })
                    recorder = made
                    onReady(made)
                }
            },
            ContextCompat.getMainExecutor(context),
        )
        onDispose {
            // Leaving the step, losing the camera permission or backing out mid
            // clip throws the half-written file away rather than uploading a stub.
            recorder?.cancel()
            provider?.unbindAll()
        }
    }

    AndroidView(factory = { textureView }, modifier = modifier)
}

/**
 * Records one clip at a time into `cacheDir/dating-selfie` and hands the file to
 * the caller. The preview is a separate use case, so it stays live throughout.
 *
 * Every call and every callback is on the main thread.
 */
class SelfieRecorder internal constructor(
    private val context: Context,
    private val videoCapture: VideoCapture<Recorder>,
    private val rotation: () -> Int,
    private val clips: SelfieClipStore = SelfieClipStore(File(context.cacheDir, CLIP_DIR)),
) {
    private val main = ContextCompat.getMainExecutor(context)
    private val handler = Handler(Looper.getMainLooper())
    private val state = SelfieRecordingState()
    private var active: Recording? = null
    private var watchdog: Runnable? = null

    val isRecording: Boolean get() = state.isBusy

    /**
     * Records for at most [durationMillis] — CameraX stops itself at the limit,
     * and a watchdog stops it if the limit never fires — then calls [onFinished]
     * with the clip, or with null when nothing usable was written.
     */
    fun record(durationMillis: Long, onFinished: (File?) -> Unit) {
        if (!state.beginRequested()) return
        val file = clips.next(System.currentTimeMillis())
        // Read at the moment of recording: the phone may have been turned since
        // the camera was bound.
        videoCapture.targetRotation = rotation()
        val options = FileOutputOptions.Builder(file).apply {
            setDurationLimitMillis(durationMillis)
            setFileSizeLimit(MAX_CLIP_BYTES)
        }.build()
        val started = runCatching {
            videoCapture.output
                .prepareRecording(context, options)
                .start(main) { event -> onEvent(event, file, onFinished) }
        }.getOrNull()
        if (started == null) {
            state.finished()
            file.delete()
            onFinished(null)
            return
        }
        active = started
        val stopAt = durationMillis + SelfieOutcomes.RECORD_WATCHDOG_MS
        watchdog = Runnable { stop() }.also { handler.postDelayed(it, stopAt) }
    }

    /** Stops early and still delivers what was recorded. */
    fun stop() {
        if (!state.stopRequested()) return
        clearWatchdog()
        active?.stop()
    }

    /**
     * Throws the in-flight clip away. Does nothing once a clip has been
     * delivered: that file belongs to the uploader.
     */
    fun cancel() {
        if (!state.cancelled()) return
        clearWatchdog()
        active?.stop()
    }

    private fun onEvent(event: VideoRecordEvent, file: File, onFinished: (File?) -> Unit) {
        when (event) {
            is VideoRecordEvent.Start -> state.started()
            is VideoRecordEvent.Finalize -> {
                active = null
                clearWatchdog()
                val deliver = state.finished()
                val usable = SelfieClipOutcome.usable(fatal = event.isFatal(), file = file)
                if (!usable || !deliver) file.delete()
                if (deliver) onFinished(if (usable) file else null)
            }
            else -> Unit
        }
    }

    private fun clearWatchdog() {
        watchdog?.let(handler::removeCallbacks)
        watchdog = null
    }

    private companion object {
        const val MAX_CLIP_BYTES = 8L * 1024 * 1024
    }
}

/**
 * The duration and size caps are how the clip is held inside the server's
 * limits, so CameraX reporting them is a success, not a failure.
 */
private fun VideoRecordEvent.Finalize.isFatal(): Boolean =
    hasError() &&
        error != VideoRecordEvent.Finalize.ERROR_DURATION_LIMIT_REACHED &&
        error != VideoRecordEvent.Finalize.ERROR_FILE_SIZE_LIMIT_REACHED

/** SD is enough for a blink check; HD is the ceiling so the upload stays small. */
private fun clipQuality(): QualitySelector = QualitySelector.fromOrderedList(
    listOf(Quality.HD, Quality.SD),
    FallbackStrategy.lowerQualityOrHigherThan(Quality.SD),
)

private fun previewResolution(): ResolutionSelector = ResolutionSelector.Builder()
    .setResolutionStrategy(
        ResolutionStrategy(
            Size(CLIP_LONG_EDGE, CLIP_SHORT_EDGE),
            ResolutionStrategy.FALLBACK_RULE_CLOSEST_LOWER_THEN_HIGHER,
        ),
    )
    .build()

private fun displayRotation(view: View): Int = view.display?.rotation ?: Surface.ROTATION_0

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
                setScale(
                    contentWidth * scale / viewWidth,
                    contentHeight * scale / viewHeight,
                    viewWidth / 2f,
                    viewHeight / 2f,
                )
            },
        )
    }

    private companion object {
        const val HALF_TURN = 180
    }
}

private const val CLIP_DIR = "dating-selfie"
private const val BIT_RATE = 2_500_000
private const val CLIP_LONG_EDGE = 1280
private const val CLIP_SHORT_EDGE = 720
