package com.us.android.feature.profile.ui

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
 * The FRONT camera for the person-verification selfie, through CameraX 1.4.1: a port of
 * `:feature:dating`'s SelfieVideoCamera.kt (features may not share code, and camera-view is
 * not in the offline cache, so the TextureView surface is provided by hand the same way).
 * A [Preview] and a [VideoCapture] are bound together, so the preview stays live while the
 * clip records. No audio: no microphone permission is involved and nothing is heard.
 *
 * UNVERIFIED ON A DEVICE (no device automation is allowed on the only handset).
 */
@Composable
internal fun PersonSelfiePreview(
    onReady: (PersonSelfieRecorder) -> Unit,
    onUnavailable: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val view = LocalView.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val textureView = remember { TextureView(context) }
    val textureProvider = remember { SelfieTextureProvider(textureView) }

    DisposableEffect(lifecycleOwner) {
        val future = ProcessCameraProvider.getInstance(context)
        var provider: ProcessCameraProvider? = null
        var recorder: PersonSelfieRecorder? = null
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
                val preview = Preview.Builder().setResolutionSelector(previewResolution()).build()
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
                    camera.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_FRONT_CAMERA, preview, videoCapture)
                }.getOrNull()
                if (bound == null) {
                    onUnavailable()
                } else {
                    val made = PersonSelfieRecorder(context, videoCapture, rotation = { displayRotation(view) })
                    recorder = made
                    onReady(made)
                }
            },
            ContextCompat.getMainExecutor(context),
        )
        onDispose {
            // Leaving the screen mid clip throws the half-written file away rather than uploading a stub.
            recorder?.cancel()
            provider?.unbindAll()
        }
    }

    AndroidView(factory = { textureView }, modifier = modifier)
}

/**
 * Records one clip at a time into `cacheDir/person-selfie` and hands the file to the caller.
 * Every call and every callback is on the main thread.
 */
internal class PersonSelfieRecorder(
    private val context: Context,
    private val videoCapture: VideoCapture<Recorder>,
    private val rotation: () -> Int,
) {
    private val directory = File(context.cacheDir, CLIP_DIR)
    private val main = ContextCompat.getMainExecutor(context)
    private val handler = Handler(Looper.getMainLooper())
    private val state = SelfieRecordingGate()
    private var active: Recording? = null
    private var watchdog: Runnable? = null

    /**
     * Records for at most [durationMillis] (CameraX stops itself at the limit; a watchdog stops
     * it if the limit never fires), then calls [onFinished] with the clip, or null when nothing
     * usable was written.
     */
    fun record(durationMillis: Long, onFinished: (File?) -> Unit) {
        if (!state.begin()) return
        directory.listFiles()?.forEach { it.delete() }
        directory.mkdirs()
        val file = File(directory, "selfie-${System.currentTimeMillis()}.mp4")
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
        watchdog = Runnable { stop() }.also { handler.postDelayed(it, durationMillis + WATCHDOG_MS) }
    }

    private fun stop() {
        if (!state.stop()) return
        clearWatchdog()
        active?.stop()
    }

    /** Throws the in-flight clip away. Does nothing once a clip has been delivered. */
    fun cancel() {
        if (!state.cancel()) return
        clearWatchdog()
        active?.stop()
    }

    private fun onEvent(event: VideoRecordEvent, file: File, onFinished: (File?) -> Unit) {
        if (event !is VideoRecordEvent.Finalize) return
        active = null
        clearWatchdog()
        val deliver = state.finished()
        val usable = !event.isFatal() && file.isFile && file.length() > 0L
        if (!usable || !deliver) file.delete()
        if (deliver) onFinished(if (usable) file else null)
    }

    private fun clearWatchdog() {
        watchdog?.let(handler::removeCallbacks)
        watchdog = null
    }

    private companion object {
        const val MAX_CLIP_BYTES = 8L * 1024 * 1024
        const val WATCHDOG_MS = 500L
    }
}

/** One clip at a time; a cancelled clip is never delivered. No camera types, so it is plain logic. */
internal class SelfieRecordingGate {
    private var busy = false
    private var stopping = false
    private var abandoned = false

    fun begin(): Boolean {
        if (busy) return false
        busy = true
        stopping = false
        abandoned = false
        return true
    }

    fun stop(): Boolean {
        if (!busy || stopping) return false
        stopping = true
        return true
    }

    fun cancel(): Boolean {
        if (!busy) return false
        abandoned = true
        stopping = true
        return true
    }

    /** True when the result should still reach the caller. */
    fun finished(): Boolean {
        val deliver = busy && !abandoned
        busy = false
        stopping = false
        return deliver
    }
}

/** The duration and size caps keep the clip small, so CameraX reporting them is a success. */
private fun VideoRecordEvent.Finalize.isFatal(): Boolean =
    hasError() &&
        error != VideoRecordEvent.Finalize.ERROR_DURATION_LIMIT_REACHED &&
        error != VideoRecordEvent.Finalize.ERROR_FILE_SIZE_LIMIT_REACHED

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

/** Feeds a [TextureView] to CameraX and keeps a centre-crop transform. */
private class SelfieTextureProvider(private val view: TextureView) :
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

private const val CLIP_DIR = "person-selfie"
private const val BIT_RATE = 2_500_000
private const val CLIP_LONG_EDGE = 1280
private const val CLIP_SHORT_EDGE = 720
