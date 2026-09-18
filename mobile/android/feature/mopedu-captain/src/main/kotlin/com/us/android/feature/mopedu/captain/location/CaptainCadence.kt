package com.us.android.feature.mopedu.captain.location

import com.us.android.feature.mopedu.captain.data.LocationTelemetryDto
import kotlin.math.asin
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.sin
import kotlin.math.sqrt

/** A monotonic clock (elapsedRealtime in the service). Injected so tests move it by hand. */
fun interface CadenceClock {
    fun nowMillis(): Long
}

/**
 * How often, and how far apart, the captain's location reaches the server.
 * The platform request sets NO minimum distance: [minDistanceMeters] is
 * applied HERE, to pings, so a stationary captain still receives fixes and
 * the no-fix watchdog can tell "standing still" from "GPS lost".
 */
data class CadenceSpec(
    val intervalMillis: Long,
    val minDistanceMeters: Double,
    val heartbeatMillis: Long,
    val highAccuracy: Boolean,
) {
    companion object {
        /** Online, no ride: 30 s / 50 m, 60 s heartbeat, balanced power. */
        val IDLE = CadenceSpec(intervalMillis = 30_000, minDistanceMeters = 50.0, heartbeatMillis = 60_000, highAccuracy = false)

        /** On a ride: 5 s / 10 m, high accuracy — the customer is watching the map. */
        val ON_RIDE = CadenceSpec(intervalMillis = 5_000, minDistanceMeters = 10.0, heartbeatMillis = 30_000, highAccuracy = true)
    }
}

/** One location fix. [atMillis] is the fix's own monotonic time, used only to drop out-of-order fixes. */
data class LocationFix(
    val latitude: Double,
    val longitude: Double,
    val accuracyMeters: Double? = null,
    val headingDegrees: Double? = null,
    val speedMps: Double? = null,
    val atMillis: Long,
)

enum class OfflineReason { TOGGLED_OFF, SIGNED_OUT, PERMISSION_REVOKED, NO_FIX, SERVER_REFUSED }

sealed interface CadenceEffect {
    data class Request(val spec: CadenceSpec) : CadenceEffect

    /** Send this fix to `POST /v1/rider/partners/me/location`. */
    data class Ping(val fix: LocationFix, val sequence: Long) : CadenceEffect

    data class GoOffline(val reason: OfflineReason) : CadenceEffect
}

/**
 * The on-duty location cadence as a pure state machine (no Android, no clock
 * of its own) — the Feast rider's LocationCadence, with the ride replacing
 * the job. [CaptainLocationService] feeds it fixes, a one-second tick and the
 * events below, and performs the effects it returns.
 */
class CaptainCadence(
    private val clock: CadenceClock,
    private val noFixTimeoutMillis: Long = NO_FIX_TIMEOUT_MILLIS,
) {
    var online: Boolean = false
        private set

    var onRide: Boolean = false
        private set

    var offlineReason: OfflineReason? = null
        private set

    val spec: CadenceSpec get() = if (onRide) CadenceSpec.ON_RIDE else CadenceSpec.IDLE

    private var latest: LocationFix? = null
    private var lastPinged: LocationFix? = null
    private var lastPingAt: Long = 0L
    private var lastFixAt: Long = 0L
    private var sequence: Long = 0L

    fun start(onRide: Boolean): List<CadenceEffect> {
        online = true
        offlineReason = null
        this.onRide = onRide
        latest = null
        lastPinged = null
        lastFixAt = clock.nowMillis()
        return listOf(CadenceEffect.Request(spec))
    }

    fun onRideChanged(onRide: Boolean): List<CadenceEffect> {
        if (!online || this.onRide == onRide) return emptyList()
        this.onRide = onRide
        val effects = mutableListOf<CadenceEffect>(CadenceEffect.Request(spec))
        latest?.let { effects += ping(it) }
        return effects
    }

    fun onFix(fix: LocationFix): List<CadenceEffect> {
        if (!online) return emptyList()
        val newest = latest
        if (newest != null && fix.atMillis < newest.atMillis) return emptyList()
        latest = fix
        val now = clock.nowMillis()
        lastFixAt = now
        val previous = lastPinged ?: return listOf(ping(fix))
        val sincePing = now - lastPingAt
        val due = sincePing >= spec.heartbeatMillis ||
            (sincePing >= spec.intervalMillis && distanceMeters(previous, fix) >= spec.minDistanceMeters)
        return if (due) listOf(ping(fix)) else emptyList()
    }

    fun onTick(): List<CadenceEffect> {
        if (!online) return emptyList()
        return if (clock.nowMillis() - lastFixAt >= noFixTimeoutMillis) stop(OfflineReason.NO_FIX) else emptyList()
    }

    fun onToggledOff(): List<CadenceEffect> = stop(OfflineReason.TOGGLED_OFF)

    fun onSignedOut(): List<CadenceEffect> = stop(OfflineReason.SIGNED_OUT)

    fun onPermissionRevoked(): List<CadenceEffect> = stop(OfflineReason.PERMISSION_REVOKED)

    private fun stop(reason: OfflineReason): List<CadenceEffect> {
        if (!online) return emptyList()
        online = false
        offlineReason = reason
        latest = null
        lastPinged = null
        return listOf(CadenceEffect.GoOffline(reason))
    }

    private fun ping(fix: LocationFix): CadenceEffect.Ping {
        lastPinged = fix
        lastPingAt = clock.nowMillis()
        return CadenceEffect.Ping(fix, ++sequence)
    }

    companion object {
        const val NO_FIX_TIMEOUT_MILLIS = 120_000L
        private const val EARTH_RADIUS_METERS = 6_371_000.0

        /** Haversine distance. */
        fun distanceMeters(a: LocationFix, b: LocationFix): Double {
            val lat1 = Math.toRadians(a.latitude)
            val lat2 = Math.toRadians(b.latitude)
            val dLat = lat2 - lat1
            val dLng = Math.toRadians(b.longitude - a.longitude)
            val h = sin(dLat / 2).pow(2) + cos(lat1) * cos(lat2) * sin(dLng / 2).pow(2)
            return 2 * EARTH_RADIUS_METERS * asin(sqrt(h.coerceIn(0.0, 1.0)))
        }
    }
}

/** The server ping for a fix: heading normalised into 0–360, accuracy and speed only when finite. */
fun CadenceEffect.Ping.toRequest(): LocationTelemetryDto = LocationTelemetryDto(
    lat = fix.latitude,
    lng = fix.longitude,
    speedMps = fix.speedMps?.takeIf { it.isFinite() && it >= 0.0 },
    heading = fix.headingDegrees?.takeIf { it.isFinite() }?.let { ((it % FULL_TURN) + FULL_TURN) % FULL_TURN },
    accuracyMeters = fix.accuracyMeters?.takeIf { it.isFinite() && it >= 0.0 },
    sequence = sequence,
)

private const val FULL_TURN = 360.0
