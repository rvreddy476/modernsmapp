package com.us.android.core.facear

import android.util.Log
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Decides, once per process, whether virtual try-on is available.
 *
 * Without a token the answer is [FaceArState.Unlicensed] from construction and
 * [ensure] does nothing at all. With one, the first [ensure] — the first entry
 * to a screen that wants a try-on — starts the native SDK if nothing else has,
 * asks the licence, and settles on [FaceArState.Ready] or a refusal. Every
 * later call is a no-op whatever the outcome, because the licence is
 * process-scoped and a failed start is not something a second attempt in the
 * same process would change.
 *
 * Shaped deliberately like the reel studio's `BanubaGate`: same five states,
 * same lazy single-shot [ensure], same "seam interface so the state machine is
 * unit-testable with a fake" arrangement. Two gates on one licence is a real
 * cost, and the thing that keeps it payable is that they are the same gate
 * twice rather than two designs.
 *
 * ## TWO BANUBA PRODUCT LINES IN ONE PROCESS
 *
 * The reel studio initialises the Banuba VIDEO EDITOR (`EditorSdk.initialize`
 * plus a Koin graph); this gate initialises Banuba FACE AR
 * (`BanubaSdkManager.initialize`). Both paths were disassembled — see
 * [BanubaFaceArSdk] — and **the licence side of that is LOW risk**: the Face
 * AR entry point is idempotent in the vendor's own code and the library load
 * is cached. So the arrangement here is deliberately plain: one process-wide
 * single-init latch ([started]), a licence read instead of a second
 * initialise when one already exists, and no `deinitialize()` on any path.
 *
 * **The hazard that is real is the CAMERA and the GL surface, not the
 * licence.** Two effect players contending for the front camera present as a
 * black preview or a crash, never as a licence error, and the thing that
 * prevents it is `FaceArSession` giving the camera back on every lifecycle
 * stop rather than anything in this class.
 *
 * **The device check, in both orders:** open the reel camera, back out, open a
 * product try-on — then the reverse.
 */
@Singleton
class FaceArGate @Inject constructor(
    config: FaceArConfig,
    private val sdk: FaceArSdk,
) {
    private val token = config.licenseToken
    private val started = AtomicBoolean(false)
    private val _state = MutableStateFlow<FaceArState>(
        if (config.isLicensed) FaceArState.Initialising else FaceArState.Unlicensed,
    )
    val state: StateFlow<FaceArState> = _state.asStateFlow()

    private val _available = MutableStateFlow(false)

    /** True exactly while [state] is [FaceArState.Ready]. Screens offer try-on on this. */
    val available: StateFlow<Boolean> = _available.asStateFlow()

    private val _licenceValid = MutableStateFlow<Boolean?>(null)

    /**
     * Validity exactly as `LicenseManager.isExpired()` answered — not as
     * [state] summarises it.
     *
     * Null means the SDK has not been asked, or was never reached because the
     * start failed first. Kept separate on purpose: [state] folds "the licence
     * says no" and "the native SDK would not start" into refusals a viewer can
     * read, and the diagnostics surface needs to tell those two apart. The
     * token itself is never exposed, here or anywhere.
     */
    val licenceValid: StateFlow<Boolean?> = _licenceValid.asStateFlow()

    /** Starts Face AR on the first call with a token; idempotent otherwise. Call on the main thread. */
    fun ensure() {
        if (token.isBlank() || !started.compareAndSet(false, true)) return
        val licence = runCatching {
            // A licence already in this process is read rather than re-issued.
            // The vendor's own initialise is idempotent, so this is tidiness
            // rather than a guard — but it also means a Face AR screen opened
            // after the reel studio does no native work at all.
            if (sdk.alreadyInitialised()) sdk.licence(token) else sdk.initialize(token)
        }.getOrElse { failure ->
            Log.w(TAG, "Face AR start failed", failure)
            move(FaceArState.Failed(failure.message ?: failure.javaClass.simpleName))
            return
        }
        if (licence == null) {
            Log.w(TAG, "no licence after initialize: token rejected (length ${token.length})")
            move(FaceArState.Failed(TOKEN_REJECTED))
            return
        }
        licence.check { valid ->
            Log.i(TAG, "Face AR licence valid=$valid")
            // Recorded BEFORE the state moves: the diagnostics surface shows
            // the SDK's own answer beside the state this app derived from it,
            // and a reader comparing the two must not see a half-applied pair.
            _licenceValid.value = valid
            move(if (valid) FaceArState.Ready else FaceArState.Invalid)
        }
    }

    private fun move(next: FaceArState) {
        _state.value = next
        _available.value = next == FaceArState.Ready
    }

    private companion object {
        const val TAG = "FaceArGate"
        const val TOKEN_REJECTED = "The licence token was rejected as empty or truncated."
    }
}
