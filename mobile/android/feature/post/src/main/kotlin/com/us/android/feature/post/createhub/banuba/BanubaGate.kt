package com.us.android.feature.post.createhub.banuba

import android.util.Log
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Decides, once per process, whether the reel flow gets the Banuba editor —
 * and, on the same answer, whether the photo flows get its Photo Editor.
 *
 * Without a token the answer is [BanubaState.Unlicensed] from construction
 * and [ensure] does nothing. With one, the first [ensure] — the first entry
 * to a flow that wants the SDK — starts the SDK graph, hands over the token
 * and asks the licence server; every later call is a no-op, whatever the
 * outcome, because the SDK is initialised for the life of the process and a
 * failed start is not something a second attempt in the same process would
 * change.
 *
 * ## ONE LICENCE, TWO EDITORS
 *
 * The Video Editor and the Photo Editor share the licence server and the
 * `EditorSdk.initialize` call, so [photoEditorAvailable] is simply
 * "[state] is [BanubaState.Ready]". Whether the token actually INCLUDES the
 * Photo Editor is something the licence check does not say: a token without
 * it opens the editor and exports nothing, which [photoEditOutcomeOf] turns
 * into a one-line failure after the fact.
 */
@Singleton
class BanubaGate @Inject constructor(
    config: BanubaConfig,
    private val sdk: BanubaSdk,
) {
    private val token = config.licenseToken
    private val started = AtomicBoolean(false)
    private val _state = MutableStateFlow<BanubaState>(
        if (config.isLicensed) BanubaState.Initialising else BanubaState.Unlicensed,
    )
    val state: StateFlow<BanubaState> = _state.asStateFlow()

    private val _photoEditorAvailable = MutableStateFlow(false)

    /** True exactly while [state] is [BanubaState.Ready]; the photo flows offer their Edit step on it. */
    val photoEditorAvailable: StateFlow<Boolean> = _photoEditorAvailable.asStateFlow()

    /** Starts the SDK on the first call with a token; idempotent otherwise. Call on the main thread. */
    fun ensure() {
        if (token.isBlank() || !started.compareAndSet(false, true)) return
        val licence = runCatching {
            sdk.startGraph()
            sdk.initialize(token)
        }.getOrElse { failure ->
            Log.w(TAG, "SDK start failed", failure)
            move(BanubaState.Failed(failure.message ?: failure.javaClass.simpleName))
            return
        }
        if (licence == null) {
            Log.w(TAG, "initialize returned null: token rejected (length ${token.length})")
            move(BanubaState.Failed(TOKEN_REJECTED))
            return
        }
        licence.check { valid ->
            Log.i(TAG, "licence state valid=$valid")
            move(if (valid) BanubaState.Ready else BanubaState.Invalid)
        }
    }

    private fun move(next: BanubaState) {
        _state.value = next
        _photoEditorAvailable.value = next == BanubaState.Ready
    }

    private companion object {
        const val TAG = "BanubaGate"
        const val TOKEN_REJECTED = "The licence token was rejected as empty or truncated."
    }
}
