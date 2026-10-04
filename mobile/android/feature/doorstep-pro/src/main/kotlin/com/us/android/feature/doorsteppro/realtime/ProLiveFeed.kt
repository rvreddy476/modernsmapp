package com.us.android.feature.doorsteppro.realtime

import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.RealtimeTokenSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

enum class LiveTransport { CONNECTING, LIVE, POLLING }

/**
 * Keeps the professional's offers and jobs fresh: realtime when it can be, polling when
 * it cannot, never neither (Feast Rider's RiderLiveFeed, copied).
 *
 * Any event on `doorstep.pro.<user_id>` triggers
 * [refresh], which re-reads `GET /pro/offers` and the jobs.
 * Server state is always re-read rather than patched from payloads. While LIVE
 * a slow reconcile poll continues; when the scoped token is refused or the
 * stream ends, polling runs every [pollMillis].
 */
class ProLiveFeed(
    private val refresh: suspend () -> Unit,
    private val subscribe: (RealtimeTokenSource) -> Flow<RealtimeEvent>,
    private val tokenSource: RealtimeTokenSource,
    private val pollMillis: Long = POLL_MILLIS,
    private val reconcileMillis: Long = RECONCILE_MILLIS,
    /** Every domain frame, before the refresh it triggers (the screen may show it at once). */
    private val onMessage: (RealtimeEvent.Message) -> Unit = {},
) {
    private val _transport = MutableStateFlow(LiveTransport.CONNECTING)
    val transport: StateFlow<LiveTransport> = _transport.asStateFlow()

    private val requests = Channel<Unit>(Channel.CONFLATED)

    fun refreshNow() {
        requests.trySend(Unit)
    }

    suspend fun run() {
        coroutineScope {
            launch { for (request in requests) refresh() }
            launch { streamLoop() }
            launch { pollLoop() }
        }
    }

    private suspend fun pollLoop() {
        refreshNow()
        var sinceLast = 0L
        while (true) {
            delay(pollMillis)
            sinceLast += pollMillis
            if (_transport.value != LiveTransport.LIVE || sinceLast >= reconcileMillis) {
                refreshNow()
                sinceLast = 0L
            }
        }
    }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun streamLoop() {
        val observed = RealtimeTokenSource { forceRefresh ->
            try {
                tokenSource.token(forceRefresh)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _transport.value = LiveTransport.POLLING
                throw e
            }
        }
        try {
            subscribe(observed).collect { event ->
                if (event is RealtimeEvent.Connected) _transport.value = LiveTransport.LIVE
                if (event is RealtimeEvent.Message) onMessage(event)
                refreshNow()
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // A terminal refusal — polling carries on.
        }
        _transport.value = LiveTransport.POLLING
    }

    companion object {
        const val POLL_MILLIS = 15_000L
        const val RECONCILE_MILLIS = 60_000L
    }
}
