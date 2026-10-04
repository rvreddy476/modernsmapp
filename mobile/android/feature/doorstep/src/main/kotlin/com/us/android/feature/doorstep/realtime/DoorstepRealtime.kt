package com.us.android.feature.doorstep.realtime

import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.RealtimeTokenSource
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import kotlinx.coroutines.flow.Flow
import java.time.Instant

/** The topic a booking's realtime token grants (asyncapi: `doorstep.booking.<booking_id>`). */
object DoorstepTopics {
    private const val BOOKING_PREFIX = "doorstep.booking."

    fun forBooking(bookingId: String): List<String> = listOf(BOOKING_PREFIX + bookingId)

    /** Whether [event] is a domain frame for [bookingId] — the screen re-reads the booking, never patches it. */
    fun isBookingChange(bookingId: String, event: RealtimeEvent): Boolean =
        event is RealtimeEvent.Message && event.topic == BOOKING_PREFIX + bookingId
}

/**
 * Doorstep's [RealtimeTokenSource]: `POST /v1/doorstep/realtime/token
 * {booking_id}` — a token scoped to that one booking's topic, minted fresh for
 * every (re)connect. A refusal throws, which the SSE client treats as a failed
 * attempt and backs off; the screen's poll keeps it fresh meanwhile.
 */
class DoorstepRealtimeTokenSource(
    private val repository: DoorstepRepository,
    private val bookingId: String,
) : RealtimeTokenSource {

    override suspend fun token(forceRefresh: Boolean): String =
        when (val result = repository.realtimeToken(bookingId)) {
            is DoorstepResult.Success -> result.value.token.takeIf { it.isNotBlank() }
                ?: throw DoorstepRealtimeTokenException(DoorstepError.Unexpected(null, "empty realtime token"))
            is DoorstepResult.Failure -> throw DoorstepRealtimeTokenException(result.error)
        }
}

class DoorstepRealtimeTokenException(val error: DoorstepError) :
    Exception("doorstep realtime token could not be issued: $error")

/** A booking's live frames. A port, so the detail screen's ViewModel tests on the JVM. */
fun interface BookingEventStream {
    fun events(bookingId: String): Flow<RealtimeEvent>
}

/** The wall clock, as a port for the hold countdown and the polling tests. */
fun interface DoorstepClock {
    fun now(): Instant

    companion object {
        val System = DoorstepClock { Instant.now() }
    }
}
