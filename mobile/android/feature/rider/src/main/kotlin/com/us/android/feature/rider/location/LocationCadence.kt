package com.us.android.feature.rider.location

import com.us.android.core.food.network.DeliveryLocationRequest
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
 * How often, and how far apart, the rider's location reaches the server.
 *
 * [intervalMillis] is also the platform request interval. The platform request
 * sets NO minimum distance: [minDistanceMeters] is applied HERE, to pings,
 * so a stationary rider still receives fixes — without them the no-fix
 * watchdog could not tell "standing still" from "GPS lost".
 */
data class CadenceSpec(
    val intervalMillis: Long,
    val minDistanceMeters: Double,
    /** A ping is sent at least this often while fixes arrive, moved or not. */
    val heartbeatMillis: Long,
    val highAccuracy: Boolean,
) {
    companion object {
        /** Online, no job: 30 s / 50 m, 60 s heartbeat, balanced power. */
        val IDLE = CadenceSpec(intervalMillis = 30_000, minDistanceMeters = 50.0, heartbeatMillis = 60_000, highAccuracy = false)

        /**
         * On a job: 8 s / 15 m, high accuracy — the customer is watching.
         * The 30 s heartbeat is a decision of this lane (the brief set none):
         * a rider waiting at a restaurant still shows as live.
         */
        val ON_JOB = CadenceSpec(intervalMillis = 8_000, minDistanceMeters = 15.0, heartbeatMillis = 30_000, highAccuracy = true)
    }
}

/** One location fix. [atMillis] is the fix's own monotonic time, used only to drop out-of-order fixes. */
data class LocationFix(
    val latitude: Double,
    val longitude: Double,
    val accuracyMeters: Double? = null,
    /** Degrees; null when the fix carries no bearing. */
    val headingDegrees: Double? = null,
    val atMillis: Long,
)

enum class OfflineReason { TOGGLED_OFF, SIGNED_OUT, PERMISSION_REVOKED, NO_FIX, SERVER_REFUSED }

sealed interface CadenceEffect {
    /** (Re)configure platform location updates. */
    data class Request(val spec: CadenceSpec) : CadenceEffect

    /** Send this fix to `POST /v1/food/delivery/location`. */
    data class Ping(val fix: LocationFix) : CadenceEffect

    /** Stop updates, tell the server the rider is offline, stop the service. */
    data class GoOffline(val reason: OfflineReason) : CadenceEffect
}

/**
 * The on-duty location cadence, as a pure state machine (no Android, no clock
 * of its own). [RiderLocationService] feeds it fixes, a one-second tick and the
 * events below, and performs the effects it returns.
 *
 * Rules:
 *  - idle online: [CadenceSpec.IDLE]; on a job: [CadenceSpec.ON_JOB];
 *  - a fix is pinged when it is the first, when [CadenceSpec.heartbeatMillis]
 *    has passed since the last ping, or when [CadenceSpec.intervalMillis] has
 *    passed AND it moved [CadenceSpec.minDistanceMeters];
 *  - LATEST FIX ONLY: nothing is queued; a fix older than the newest seen is
 *    dropped, and the service conflates pings so a slow network sends the
 *    newest fix, never a backlog;
 *  - auto-offline when toggled off, signed out, permission revoked, or
 *    [noFixTimeoutMillis] (2 min) pass without a fix.
 */
class LocationCadence(
    private val clock: CadenceClock,
    private val noFixTimeoutMillis: Long = NO_FIX_TIMEOUT_MILLIS,
) {
    var online: Boolean = false
        private set

    var onJob: Boolean = false
        private set

    var offlineReason: OfflineReason? = null
        private set

    val spec: CadenceSpec get() = if (onJob) CadenceSpec.ON_JOB else CadenceSpec.IDLE

    private var latest: LocationFix? = null
    private var lastPinged: LocationFix? = null
    private var lastPingAt: Long = 0L
    private var lastFixAt: Long = 0L

    /** Going on duty. The no-fix watchdog starts now, so the first fix has two minutes to arrive. */
    fun start(onJob: Boolean): List<CadenceEffect> {
        online = true
        offlineReason = null
        this.onJob = onJob
        latest = null
        lastPinged = null
        lastFixAt = clock.nowMillis()
        return listOf(CadenceEffect.Request(spec))
    }

    /** A job was accepted or finished. Re-configures the platform and sends the latest fix at once. */
    fun onJobChanged(onJob: Boolean): List<CadenceEffect> {
        if (!online || this.onJob == onJob) return emptyList()
        this.onJob = onJob
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

    /** Called every second. Only the watchdog lives here: heartbeats ride on fresh fixes. */
    fun onTick(): List<CadenceEffect> {
        if (!online) return emptyList()
        return if (clock.nowMillis() - lastFixAt >= noFixTimeoutMillis) stop(OfflineReason.NO_FIX) else emptyList()
    }

    fun onToggledOff(): List<CadenceEffect> = stop(OfflineReason.TOGGLED_OFF)

    fun onSignedOut(): List<CadenceEffect> = stop(OfflineReason.SIGNED_OUT)

    fun onPermissionRevoked(): List<CadenceEffect> = stop(OfflineReason.PERMISSION_REVOKED)

    fun onServerRefused(): List<CadenceEffect> = stop(OfflineReason.SERVER_REFUSED)

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
        return CadenceEffect.Ping(fix)
    }

    companion object {
        const val NO_FIX_TIMEOUT_MILLIS = 120_000L
        private const val EARTH_RADIUS_METERS = 6_371_000.0

        /** Haversine distance. Accurate to well under a metre at these ranges. */
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

/**
 * The server ping for a fix. The heading rides along only when the fix has one,
 * normalised into the 0–360 range the route accepts; accuracy likewise.
 */
fun LocationFix.toRequest(): DeliveryLocationRequest = DeliveryLocationRequest(
    latitude = latitude,
    longitude = longitude,
    accuracyMeters = accuracyMeters?.takeIf { it.isFinite() && it >= 0.0 },
    heading = headingDegrees?.takeIf { it.isFinite() }?.let { ((it % FULL_TURN) + FULL_TURN) % FULL_TURN },
)

private const val FULL_TURN = 360.0
