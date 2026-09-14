package com.us.android.feature.feast.tracking

import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.realtime.RealtimeEvent
import kotlinx.coroutines.flow.Flow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import java.time.Instant
import java.time.format.DateTimeParseException
import kotlin.math.asin
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.sin
import kotlin.math.sqrt

/** Device time. A port so "last updated" and ETA text are testable. */
fun interface FeastClock {
    fun now(): Instant

    companion object {
        val System = FeastClock { Instant.now() }
    }
}

/** The order's live topic. Bound to notification-service SSE in FeastModule. */
fun interface OrderEventStream {
    fun events(orderId: String): Flow<RealtimeEvent>
}

/** The topic an `{scope:"order", id}` token grants (food-service realtime_topics.go orderTopic). */
object OrderTopics {
    fun forOrder(orderId: String): List<String> = listOf("food.order.$orderId")
}

/** Where the rider is, as of [recordedAt] (the rider's ping time, not receipt time). */
data class RiderPosition(
    val latitude: Double,
    val longitude: Double,
    val heading: Double?,
    val recordedAt: Instant,
)

/**
 * One `rider.location` frame on `food.order.<id>`
 * (service/rider_presence_worker.go riderLocationPayload):
 * `{order_id, lat, lng, heading?, recorded_at, eta_at?, eta_source?}`.
 */
data class RiderLocationFrame(
    val orderId: String,
    val position: RiderPosition,
    val etaAt: Instant?,
    val etaSource: String?,
)

/** What the tracking screen renders. Built from the order read plus live frames. */
data class TrackingModel(
    val order: FeastOrderDto? = null,
    val rider: RiderPosition? = null,
    val etaAt: Instant? = null,
    val etaSource: String? = null,
    val customerLatitude: Double? = null,
    val customerLongitude: Double? = null,
    /** Device time of the last change that came from the server (read or frame). */
    val lastUpdatedAt: Instant? = null,
)

/**
 * The live half of tracking, pure: parse a frame, apply it.
 *
 * STALE FRAMES ARE IGNORED. The SSE stream resumes from a Redis stream id after
 * a reconnect, and the order read can land after a frame, so frames can arrive
 * out of order; a frame whose `recorded_at` is not newer than the position
 * already shown would move the rider backwards and is dropped. A frame for
 * another order — which the topic token should make impossible — is dropped too.
 */
object TrackingFrames {

    const val RIDER_LOCATION = "rider.location"

    /** The frame in [event], or null when it is not a well-formed rider.location for [orderId]. */
    fun parse(orderId: String, event: RealtimeEvent): RiderLocationFrame? {
        if (event !is RealtimeEvent.Message || event.eventType != RIDER_LOCATION) return null
        val data = event.data as? JsonObject ?: return null
        val frameOrder = data.string("order_id") ?: return null
        if (frameOrder != orderId) return null
        val lat = data.double("lat") ?: return null
        val lng = data.double("lng") ?: return null
        val recordedAt = data.string("recorded_at")?.let(::instantOrNull) ?: return null
        return RiderLocationFrame(
            orderId = frameOrder,
            position = RiderPosition(lat, lng, data.double("heading"), recordedAt),
            etaAt = data.string("eta_at")?.let(::instantOrNull),
            etaSource = data.string("eta_source"),
        )
    }

    /** [model] with [frame] applied, or [model] unchanged when the frame is stale. */
    fun apply(model: TrackingModel, frame: RiderLocationFrame, receivedAt: Instant): TrackingModel {
        val current = model.rider
        if (current != null && !frame.position.recordedAt.isAfter(current.recordedAt)) return model
        return model.copy(
            rider = frame.position,
            // A frame without an ETA keeps the last one: the server attaches it
            // only when the order has one to promise.
            etaAt = frame.etaAt ?: model.etaAt,
            etaSource = if (frame.etaAt != null) frame.etaSource else model.etaSource,
            lastUpdatedAt = receivedAt,
        )
    }

    /** Whether [event] is some other change on the order's topic, worth re-reading the order for. */
    fun isOrderChange(orderId: String, event: RealtimeEvent): Boolean =
        event is RealtimeEvent.Message && event.eventType != RIDER_LOCATION && event.topic == "food.order.$orderId"

    private fun JsonObject.string(key: String): String? = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull

    private fun JsonObject.double(key: String): Double? = (this[key] as? JsonPrimitive)?.takeIf { !it.isString }?.doubleOrNull
}

internal fun instantOrNull(text: String): Instant? = try {
    Instant.parse(text)
} catch (e: DateTimeParseException) {
    null
}

/** The delivery code, only while the food is with the rider (DeliveryCodeVisible on the server). */
object DeliveryCodeRule {
    val VISIBLE_STATUSES: Set<String> = setOf("PICKED_UP", "OUT_FOR_DELIVERY")

    fun visibleCode(order: FeastOrderDto): String? =
        order.deliveryCode?.takeIf { order.status in VISIBLE_STATUSES && it.isNotBlank() }
}

/** Straight-line kilometres between two points — for "1.2 km away", never for money or ETA. */
fun distanceKm(lat1: Double, lng1: Double, lat2: Double, lng2: Double): Double {
    val dLat = Math.toRadians(lat2 - lat1)
    val dLng = Math.toRadians(lng2 - lng1)
    val a = sin(dLat / 2).pow(2) + cos(Math.toRadians(lat1)) * cos(Math.toRadians(lat2)) * sin(dLng / 2).pow(2)
    return 2 * EARTH_RADIUS_KM * asin(sqrt(a))
}

private const val EARTH_RADIUS_KM = 6371.0
