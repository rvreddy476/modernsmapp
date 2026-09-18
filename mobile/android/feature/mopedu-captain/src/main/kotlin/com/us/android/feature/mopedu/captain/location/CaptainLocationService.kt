package com.us.android.feature.mopedu.captain.location

import android.Manifest
import android.annotation.SuppressLint
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.location.Location
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.google.android.gms.location.FusedLocationProviderClient
import com.google.android.gms.location.LocationCallback
import com.google.android.gms.location.LocationRequest
import com.google.android.gms.location.LocationResult
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.SessionState
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.mopedu.captain.R
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import dagger.hilt.android.AndroidEntryPoint
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import javax.inject.Inject

/**
 * On duty: a foreground service of type `location` that feeds the captain's
 * position to `POST /v1/rider/partners/me/location` on [CaptainCadence]'s rules.
 *
 * Started ONLY by Home after the prominent disclosure, the foreground location
 * grant and the server accepting the captain online (see GoOnlineFlow). It
 * never asks for background location: a location foreground service keeps
 * while-in-use access for as long as it runs, and its ongoing notification on
 * `captain_on_duty` is the captain's constant signal that location is shared.
 * The Feast rider's RiderLocationService, with the ride replacing the job.
 */
@AndroidEntryPoint
class CaptainLocationService : Service() {

    @Inject
    lateinit var captain: MopeduCaptainRepository

    @Inject
    lateinit var duty: CaptainDuty

    @Inject
    lateinit var session: SessionStateProvider

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val cadence = CaptainCadence(clock = { SystemClock.elapsedRealtime() })
    private val pings = Channel<CadenceEffect.Ping>(Channel.CONFLATED)
    private var client: FusedLocationProviderClient? = null
    private var running = false

    private val callback = object : LocationCallback() {
        override fun onLocationResult(result: LocationResult) {
            result.lastLocation?.let { perform(cadence.onFix(it.toFix())) }
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when {
            intent?.action == ACTION_STOP -> if (running) perform(cadence.onToggledOff()) else stopSelf()
            !running -> begin()
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        client?.removeLocationUpdates(callback)
        if (running) duty.report(DutyStatus.Offline(cadence.offlineReason))
        scope.cancel()
        super.onDestroy()
    }

    private fun begin() {
        val started = runCatching {
            ServiceCompat.startForeground(this, NOTIFICATION_ID, onDutyNotification(), ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION)
        }.isSuccess
        if (!started || !hasLocationPermission()) {
            duty.report(DutyStatus.Offline(OfflineReason.PERMISSION_REVOKED))
            stopSelf()
            return
        }
        running = true
        client = LocationServices.getFusedLocationProviderClient(this)
        duty.report(DutyStatus.Online(lastPingOk = null))
        perform(cadence.start(onRide = duty.onRide.value))

        scope.launch {
            for (ping in pings) {
                val ok = captain.sendLocation(ping.toRequest()) is CaptainResult.Success
                if (running) duty.report(DutyStatus.Online(lastPingOk = ok))
            }
        }
        scope.launch { duty.onRide.collect { perform(cadence.onRideChanged(it)) } }
        scope.launch {
            duty.stopRequests.collect { reason ->
                perform(if (reason == OfflineReason.SIGNED_OUT) cadence.onSignedOut() else cadence.onToggledOff())
            }
        }
        scope.launch {
            session.sessionState.collect { state ->
                if (state !is SessionState.Authenticated && state != SessionState.Unknown) perform(cadence.onSignedOut())
            }
        }
        scope.launch {
            while (isActive) {
                delay(TICK_MILLIS)
                perform(if (hasLocationPermission()) cadence.onTick() else cadence.onPermissionRevoked())
            }
        }
    }

    private fun perform(effects: List<CadenceEffect>) {
        for (effect in effects) {
            when (effect) {
                is CadenceEffect.Request -> requestUpdates(effect.spec)
                is CadenceEffect.Ping -> pings.trySend(effect)
                is CadenceEffect.GoOffline -> goOffline(effect.reason)
            }
        }
    }

    @SuppressLint("MissingPermission") // checked in begin() and on every tick
    private fun requestUpdates(spec: CadenceSpec) {
        val fused = client ?: return
        fused.removeLocationUpdates(callback)
        val priority = if (spec.highAccuracy) Priority.PRIORITY_HIGH_ACCURACY else Priority.PRIORITY_BALANCED_POWER_ACCURACY
        val request = LocationRequest.Builder(priority, spec.intervalMillis)
            .setMinUpdateIntervalMillis(spec.intervalMillis / 2)
            .setMinUpdateDistanceMeters(0f)
            .setMaxUpdateDelayMillis(spec.intervalMillis)
            .setWaitForAccurateLocation(false)
            .build()
        try {
            fused.requestLocationUpdates(request, callback, Looper.getMainLooper())
        } catch (e: SecurityException) {
            perform(cadence.onPermissionRevoked())
        }
    }

    private fun goOffline(reason: OfflineReason) {
        client?.removeLocationUpdates(callback)
        running = false
        duty.report(DutyStatus.Offline(reason))
        scope.launch {
            // After a sign-out the session is gone; Home already told the server.
            if (reason != OfflineReason.SIGNED_OUT) {
                withContext(NonCancellable) { captain.setOnline(false) }
            }
            ServiceCompat.stopForeground(this@CaptainLocationService, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    private fun hasLocationPermission(): Boolean =
        ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
            ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun onDutyNotification() = NotificationCompat.Builder(this, NotificationChannelSpec.CAPTAIN_ON_DUTY.id)
        .setSmallIcon(R.drawable.ic_stat_captain_on_duty)
        .setContentTitle("You're online")
        .setContentText("Sharing your location while you're on duty")
        .setOngoing(true)
        .setOnlyAlertOnce(true)
        .setCategory(NotificationCompat.CATEGORY_SERVICE)
        .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
        .apply {
            packageManager.getLaunchIntentForPackage(packageName)?.let { launch ->
                setContentIntent(
                    PendingIntent.getActivity(this@CaptainLocationService, 0, launch, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT),
                )
            }
        }
        .build()

    companion object {
        private const val ACTION_STOP = "com.us.android.feature.mopedu.captain.location.STOP"
        private const val NOTIFICATION_ID = 4_201
        private const val TICK_MILLIS = 1_000L

        /** Call only after the disclosure, the grant and the server's yes. */
        fun start(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, CaptainLocationService::class.java))
        }
    }
}

/** A platform fix as the cadence reads it: bearing, speed and accuracy only when the fix has them. */
internal fun Location.toFix(): LocationFix = LocationFix(
    latitude = latitude,
    longitude = longitude,
    accuracyMeters = if (hasAccuracy()) accuracy.toDouble() else null,
    headingDegrees = if (hasBearing()) bearing.toDouble() else null,
    speedMps = if (hasSpeed()) speed.toDouble() else null,
    atMillis = elapsedRealtimeNanos / NANOS_PER_MILLI,
)

private const val NANOS_PER_MILLI = 1_000_000L
