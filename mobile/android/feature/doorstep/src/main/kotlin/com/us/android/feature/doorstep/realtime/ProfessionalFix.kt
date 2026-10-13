package com.us.android.feature.doorstep.realtime

import com.us.android.core.realtime.RealtimeEvent
import kotlinx.serialization.json.*
import java.time.Instant

data class ProfessionalFix(val lat: Double, val lng: Double, val etaMinutes: Int?, val at: Instant) {
    fun isFresh(now: Instant): Boolean {
        val age = java.time.Duration.between(at, now).toMillis()
        return lat.isFinite() && lng.isFinite() && lat in -90.0..90.0 && lng in -180.0..180.0 && age in 0..120_000
    }
}

fun professionalFix(bookingId: String, event: RealtimeEvent): ProfessionalFix? = runCatching {
    val frame = event as? RealtimeEvent.Message ?: return null
    if (frame.topic != "doorstep.booking.$bookingId" || frame.eventType !in setOf("doorstep.booking.pro_location", "pro_location")) return null
    val data = frame.data.jsonObject
    if (data["booking_id"]?.jsonPrimitive?.contentOrNull != bookingId) return null
    ProfessionalFix(
        data.getValue("lat").jsonPrimitive.double,
        data.getValue("lng").jsonPrimitive.double,
        data["eta_minutes"]?.jsonPrimitive?.intOrNull?.takeIf { it >= 0 },
        Instant.parse(data.getValue("at").jsonPrimitive.content),
    ).takeIf { it.lat.isFinite() && it.lng.isFinite() && it.lat in -90.0..90.0 && it.lng in -180.0..180.0 }
}.getOrNull()
