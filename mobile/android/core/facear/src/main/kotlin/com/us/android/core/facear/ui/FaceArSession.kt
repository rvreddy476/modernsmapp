package com.us.android.core.facear.ui

import android.content.Context
import android.graphics.Bitmap
import android.util.Log
import android.view.Surface
import com.banuba.sdk.camera.Facing
import com.banuba.sdk.effect_player.Effect
import com.banuba.sdk.effect_player.EffectActivatedListener
import com.banuba.sdk.effect_player.ErrorListener
import com.banuba.sdk.effect_player.HintListener
import com.banuba.sdk.entity.ContentRatioParams
import com.banuba.sdk.entity.RecordedVideoInfo
import com.banuba.sdk.manager.BanubaSdkManager
import com.banuba.sdk.manager.IEventCallback
import com.us.android.core.facear.TRY_ON_LOG_TAG
import com.us.android.core.facear.TryOnApplyOutcome
import com.us.android.core.facear.TryOnCallPath
import com.us.android.core.facear.TryOnJsCall
import com.us.android.core.facear.TryOnJsEngine
import com.us.android.core.facear.TryOnLoadOutcome
import com.us.android.core.facear.applyThrough
import com.us.android.core.facear.parseAck
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import com.banuba.sdk.types.Data as BanubaFrameData

/**
 * Everything the session OBSERVED, as opposed to everything it hoped.
 *
 * Held as a flow rather than returned from the calls that produce it, because
 * three of these arrive on vendor callbacks — an error, a hint, an activation
 * — at moments nobody asked for them, and a return value cannot carry those.
 */
data class FaceArSessionReport(
    val surfaceReady: Boolean = false,
    val load: TryOnLoadOutcome = TryOnLoadOutcome.NotAttempted,
    /** The vendor's own words. Never paraphrased — see [TryOnLoadOutcome]. */
    val sdkError: String? = null,
    val sdkHint: String? = null,
    val activated: String? = null,
    val apply: TryOnApplyOutcome? = null,

    /**
     * What the loaded bundle said about its own ability to draw, or null when
     * it does not implement `momentumTryOnReady`.
     */
    val drawable: Boolean? = null,

    /**
     * True when a loaded effect was deliberately UNLOADED because it said it
     * cannot draw.
     *
     * Unloading puts the plain camera back. Leaving such an effect loaded
     * renders its empty scene instead, which on a handset is a black
     * rectangle — and the SDK calls that a success.
     */
    val unloadedAsBlank: Boolean = false,
)

/**
 * One try-on screen's live camera and effect, as a plain object the composable
 * can hold in `remember`.
 *
 * ## WHY THE VENDOR MANAGER IS NOT TOUCHED ANYWHERE ELSE
 *
 * `BanubaSdkManager` is a stateful native object with a surface, a camera, a
 * render thread and a `recycle()` that must be called exactly once. Spreading
 * those calls across a composable's effects is how a rotated screen ends up
 * with two render threads and a black preview. Everything lives here, in one
 * class with a documented order, and [FaceArTryOnSurface] only calls it.
 *
 * ## THE ORDER, WHICH IS NOT OPTIONAL — AND WHERE THE EFFECT GOES IN IT
 *
 * ```
 * attach(surface) → surfaceCreated() → surfaceChanged(f,w,h) → resume()
 *                                                            ↕  (lifecycle)
 *                                        loadEffect()          pause()
 * surfaceDestroyed() → release()
 * ```
 *
 * **`loadEffect` comes AFTER the surface exists, and that is load-bearing.**
 * The SDK carries the message "No surface, failed to create effect: %s", and
 * `BanubaSdkManager.loadEffect(path, true)` posts a task to the render thread
 * and blocks on it — a render thread with no EGL surface has no GL context to
 * compile an effect's shaders in. An effect loaded on composition, before the
 * `SurfaceView` has called back, is exactly the shape of "a camera opens and
 * nothing is ever drawn on it". [surfaceCreated] is what the host waits for.
 *
 * `recycle()` is the last call and is idempotent here; the licence itself is
 * process-scoped and is NEVER deinitialised — see `BanubaFaceArSdk`.
 *
 * ## THE HAZARD THIS CLASS EXISTS TO PREVENT: CAMERA AND GL CONTENTION
 *
 * The reel studio's Banuba Video Editor creates an effect player of its own,
 * inside its own Koin graph. This class creates a second one. **Two effect
 * players contending for the front camera and a GL context is the realistic
 * failure in this app, and it presents as a black preview or a crash — never
 * as a licence error.**
 *
 * So the rule this class enforces is structural: **a try-on screen holds no
 * camera the moment it is not resumed.** [pause] closes the camera and pauses
 * the player on every lifecycle stop, not only on dispose; [release] recycles
 * the native render thread when the destination leaves.
 *
 * ## EVERY REFUSAL IS RECORDED, NOT ONLY SWALLOWED
 *
 * These are JNI calls into a licensed SDK, and a try-on that cannot start must
 * leave the viewer on a product page with a sentence rather than take the
 * process down — so they are all guarded. What changed is that a guarded
 * failure now lands in [report] as well as in the log, because the device this
 * ships to has no logcat attached and a swallowed exception there is a bug
 * nobody can see.
 *
 * ## NOT DEVICE-VERIFIED
 *
 * No unit test covers this class and none can: every method is a native call
 * against a camera. It is deliberately thin for that reason — the decisions
 * worth testing (which effect, which JS call, which entry point, how to read
 * the answer back) are pure functions in `:core:facear`'s root package.
 */
class FaceArSession(
    context: Context,
    private val onPhoto: (Bitmap) -> Unit,
    private val onCameraFailed: (String) -> Unit,
) {
    private val manager = BanubaSdkManager(context)
    private var effect: Effect? = null
    private var released = false

    private val _report = MutableStateFlow(FaceArSessionReport())

    /** What this session has observed so far. Rendered by the diagnostics panel. */
    val report: StateFlow<FaceArSessionReport> = _report.asStateFlow()

    /** Front by default: a try-on is a mirror, and nobody tries glasses on the back camera. */
    var facing: Facing = Facing.FRONT
        private set

    /**
     * The vendor's error channel, kept verbatim.
     *
     * This is the single highest-value line on the diagnostics panel. The
     * first real failure of this screen was the SDK saying
     * "Malformed config.json: missing the required property 'scene'", and
     * nothing else in the system knew it.
     */
    private val errors = ErrorListener { where, what ->
        val text = "$where: $what"
        Log.e(TRY_ON_LOG_TAG, "SDK error — $text")
        _report.update { it.copy(sdkError = text) }
    }

    private val hints = HintListener { hint ->
        Log.i(TRY_ON_LOG_TAG, "SDK hint — $hint")
        _report.update { it.copy(sdkHint = hint) }
    }

    private val activations = EffectActivatedListener { url ->
        Log.i(TRY_ON_LOG_TAG, "effect activated — $url")
        _report.update { it.copy(activated = url) }
    }

    init {
        manager.setCallback(object : IEventCallback {
            // The high-quality still from takePhoto — the capture button's
            // result. onScreenshotReady is the low-quality sibling and is not
            // what a shareable photo should be made of.
            override fun onHQPhotoReady(bitmap: Bitmap) = onPhoto(bitmap)

            override fun onCameraOpenError(error: Throwable) {
                Log.w(TRY_ON_LOG_TAG, "camera open failed", error)
                onCameraFailed(CAMERA_FAILED)
            }

            override fun onCameraStatus(opened: Boolean) = Unit
            override fun onScreenshotReady(bitmap: Bitmap) = Unit
            override fun onVideoRecordingFinished(info: RecordedVideoInfo) = Unit
            override fun onVideoRecordingStatusChange(started: Boolean) = Unit
            override fun onImageProcessed(bitmap: Bitmap) = Unit
            override fun onFrameRendered(data: BanubaFrameData, width: Int, height: Int) = Unit
        })
        guarded("listeners") {
            manager.effectManager.addErrorListener(errors)
            manager.effectManager.addHintListener(hints)
            manager.effectManager.addEffectActivatedListener(activations)
        }
    }

    /** Binds the render output to a surface that has just been created. */
    fun attach(surface: Surface) = guarded("attach") {
        manager.attachSurface(surface)
        manager.onSurfaceCreated()
        _report.update { it.copy(surfaceReady = true) }
    }

    /**
     * The surface's new geometry.
     *
     * The three ints are the vendor's and are passed straight through from
     * `SurfaceHolder.Callback.surfaceChanged`, in its order (format, width,
     * height). If a preview ever comes out stretched or rotated on a device,
     * this argument order is the first thing to try swapping — the vendor
     * documents the signature and not the meaning.
     */
    fun surfaceChanged(format: Int, width: Int, height: Int) = guarded("surfaceChanged") {
        manager.onSurfaceChanged(format, width, height)
    }

    /** The surface is going away. Called before [release] on a normal teardown. */
    fun surfaceDestroyed() = guarded("surfaceDestroyed") {
        _report.update { it.copy(surfaceReady = false) }
        manager.onSurfaceDestroyed()
        manager.releaseSurface()
    }

    /** Camera on, effect playing. Safe to call repeatedly. */
    fun resume() = guarded("resume") {
        manager.openCamera()
        manager.effectPlayerPlay()
    }

    /**
     * Camera off, effect paused — on every lifecycle stop, not only on
     * dispose. A backgrounded screen holding the camera is both a battery
     * drain and the reason another app's camera fails to open.
     */
    fun pause() = guarded("pause") {
        manager.effectPlayerPause()
        manager.closeCamera()
    }

    /**
     * Loads the effect at [loadPath], replacing whatever was on, and records
     * what happened.
     *
     * TWO ENTRY POINTS, TRIED IN ORDER, because they are not the same call.
     * `BanubaSdkManager.loadEffect(path, true)` wraps
     * `EffectManager.load(path)` in a task posted to the render thread and
     * blocks on the future — convenient, but it swallows the difference
     * between "the effect refused to parse" and "there was no render thread to
     * ask", and it returns null on an `ExecutionException` after logging at a
     * level this app does not see. `EffectManager.load(path)` is the call
     * underneath it. If the wrapper gives nothing, the bare call is asked, and
     * the report says which answered.
     *
     * Call OFF the main thread: the synchronous form blocks until the render
     * thread has finished, which is an ANR on a slow first shader compile.
     */
    fun loadEffect(loadPath: String, probe: String? = null) {
        if (released) return
        unloadEffect(asBlank = false)

        val attempts = linkedMapOf<String, String>()
        for ((via, load) in loaders(loadPath)) {
            val result = runCatching(load)
            val loaded = result.getOrNull()
            if (loaded != null) {
                effect = loaded
                val status = runCatching { loaded.status()?.name }.getOrNull()
                Log.i(TRY_ON_LOG_TAG, "loadEffect('$loadPath') ok via $via, status=$status")
                _report.update { it.copy(load = TryOnLoadOutcome.Loaded(via, status)) }
                probe?.let { expression -> settleDrawable(loaded, expression) }
                return
            }
            attempts[via] = result.exceptionOrNull()
                ?.let { it.message ?: it.javaClass.simpleName }
                ?: NULL_EFFECT
        }

        val reason = attempts.entries.joinToString("; ") { "${it.key} → ${it.value}" }
        Log.w(TRY_ON_LOG_TAG, "loadEffect('$loadPath') produced no Effect — $reason")
        _report.update {
            it.copy(load = TryOnLoadOutcome.Failed(attempts.keys.joinToString(", "), reason))
        }
    }

    /**
     * Asks a freshly loaded effect whether it can draw, and takes it back out
     * if it says no.
     *
     * This is the guard for the failure the SDK calls a success: an effect
     * whose scene did not give it what it needs loads, activates, and then
     * renders an empty scene OVER the camera — a black rectangle, no error, no
     * log line. Unloading restores the plain camera, which is an honest partial
     * the shopper can still use.
     *
     * A bundle that does not implement `momentumTryOnReady` answers `unknown`,
     * which is null, which changes nothing. Silence is not a refusal.
     */
    private fun settleDrawable(loaded: Effect, probe: String) {
        val ack = parseAck(runCatching { loaded.evalJsSync(probe) }.getOrNull())
        _report.update { it.copy(drawable = ack?.drawable) }
        if (ack?.drawable != false) return
        Log.w(TRY_ON_LOG_TAG, "effect says it cannot draw (${ack.raw}); unloading so the camera shows")
        unloadEffect(asBlank = true)
    }

    /** Takes the current effect back out of the player, if there is one. */
    private fun unloadEffect(asBlank: Boolean) {
        guarded("unloadEffect") { effect?.let { manager.unloadEffect(it) } }
        effect = null
        if (asBlank) _report.update { it.copy(unloadedAsBlank = true) }
    }

    private fun loaders(loadPath: String): List<Pair<String, () -> Effect?>> = listOf(
        "BanubaSdkManager.loadEffect(path, sync)" to { manager.loadEffect(loadPath, true) },
        "EffectManager.load(path)" to { manager.effectManager.load(loadPath) },
    )

    /**
     * Applies a variant to the loaded effect, through every entry point that
     * could work, and records which one was acknowledged.
     *
     * The ladder itself is [applyThrough] — a pure function over the
     * [TryOnJsEngine] seam — so the ORDER, the failure handling and the
     * reading of the answer are all decided in unit tests rather than on a
     * handset. This method is the four lines that hand it a real Effect.
     *
     * Call OFF the main thread: [probe] goes through `evalJsSync`, which
     * blocks until the effect's interpreter has run.
     */
    fun apply(call: TryOnJsCall, probe: String?) {
        if (released) return
        val loaded = effect ?: run {
            // Not silence: "the shade was sent before an effect existed" is a
            // different bug from "the shade was sent and ignored", and the
            // panel has to be able to say which.
            Log.w(TRY_ON_LOG_TAG, "apply(${call.script}) with no effect loaded")
            _report.update {
                it.copy(
                    apply = TryOnApplyOutcome(
                        attempted = emptyList(),
                        acknowledgedBy = null,
                        ack = null,
                        failures = mapOf(TryOnCallPath.CALL_JS_METHOD to NO_EFFECT_LOADED),
                    ),
                )
            }
            return
        }
        val outcome = applyThrough(call, probe, EffectEngine(loaded))
        Log.i(TRY_ON_LOG_TAG, "apply(${call.script}) → $outcome")
        _report.update { it.copy(apply = outcome) }
    }

    /** Swaps front/back and returns the camera now in use. */
    fun flip(): Facing {
        val next = if (facing == Facing.FRONT) Facing.BACK else Facing.FRONT
        guarded("flip") { manager.setCameraFacing(next) }
        facing = next
        return next
    }

    /** Asks for a high-quality still; it arrives on the `onPhoto` callback. */
    fun capture(width: Int, height: Int) = guarded("capture") {
        manager.takePhoto(ContentRatioParams(width, height, false))
    }

    /**
     * The last call. Releases the native render thread and camera.
     *
     * It does NOT deinitialise the licence: another surface in this process —
     * a reel export, a second try-on — may still be using it, and the licence
     * is process-scoped by design.
     */
    fun release() {
        if (released) return
        released = true
        runCatching {
            manager.effectManager.removeErrorListener(errors)
            manager.effectManager.removeHintListener(hints)
            manager.effectManager.removeEffectActivatedListener(activations)
        }.onFailure { Log.w(TRY_ON_LOG_TAG, "listener removal failed", it) }
        runCatching {
            effect?.let { manager.unloadEffect(it) }
            manager.recycle()
        }.onFailure { Log.w(TRY_ON_LOG_TAG, "release failed", it) }
        effect = null
    }

    /**
     * Every native call, wrapped — and now named, so a failure is a line
     * somebody can read instead of one generic "call failed".
     */
    private inline fun guarded(what: String, block: () -> Unit) {
        if (released) return
        runCatching(block).onFailure { failure ->
            Log.w(TRY_ON_LOG_TAG, "Face AR $what failed", failure)
            _report.update { report ->
                report.copy(sdkError = report.sdkError ?: "$what: ${failure.message ?: failure}")
            }
        }
    }

    /** The vendor `Effect` behind the pure ladder's seam. */
    private class EffectEngine(private val effect: Effect) : TryOnJsEngine {
        override fun callJsMethod(method: String, arguments: String) =
            effect.callJsMethod(method, arguments)

        override fun evalJs(script: String) = effect.evalJs(script) { /* no result to read */ }

        override fun evalJsSync(script: String): String? = effect.evalJsSync(script)
    }

    private companion object {
        const val CAMERA_FAILED = "The camera could not be opened."
        const val NULL_EFFECT = "returned null"
        const val NO_EFFECT_LOADED = "no effect is loaded"
    }
}
