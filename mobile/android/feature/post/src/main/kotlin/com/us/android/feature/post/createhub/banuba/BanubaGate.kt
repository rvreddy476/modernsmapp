package com.us.android.feature.post.createhub.banuba

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Decides, once per process, whether the reel flow gets the Banuba editor.
 *
 * Without a token the answer is [BanubaState.Unlicensed] from construction
 * and [ensure] does nothing. With one, the first [ensure] — the reel flow's
 * first entry — starts the SDK graph, hands over the token and asks the
 * licence server; every later call is a no-op, whatever the outcome, because
 * the SDK is initialised for the life of the process and a failed start is
 * not something a second attempt in the same process would change.
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

    /** Starts the SDK on the first call with a token; idempotent otherwise. Call on the main thread. */
    fun ensure() {
        if (token.isBlank() || !started.compareAndSet(false, true)) return
        val licence = runCatching {
            sdk.startGraph()
            sdk.initialize(token)
        }.getOrElse { failure ->
            _state.value = BanubaState.Failed(failure.message ?: failure.javaClass.simpleName)
            return
        }
        if (licence == null) {
            _state.value = BanubaState.Failed(TOKEN_REJECTED)
            return
        }
        licence.check { valid -> _state.value = if (valid) BanubaState.Ready else BanubaState.Invalid }
    }

    private companion object {
        const val TOKEN_REJECTED = "The licence token was rejected as empty or truncated."
    }
}
