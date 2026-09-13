package com.us.android.feature.rider.location

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
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.model.SessionState
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.rider.R
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
 * On duty: a foreground service of type `location` that feeds the rider's
 * position to `POST /v1/food/delivery/location` on [LocationCadence]'s rules.
 *
 * Started ONLY by Home after the prominent disclosure, the foreground location
 * grant and the server accepting availability (see GoOnlineFlow). It never asks
 * for background location: a location foreground service keeps while-in-use
 * access for as long as it runs, and its ongoing notification on
 * `rider_on_duty` is the rider's constant signal that location is being shared.
 *
 * Every cadence decision is the pure state machine's; this class only performs
 * its effects. All callbacks run on the main thread, so the machine is never
 * touched concurrently. Pings go through a CONFLATED channel: a slow network
 * sends the newest fix, never a backlog.
 */
@AndroidEntryPoint
class RiderLocationService : Service() {

    @Inject
    lateinit var rider: RiderRepository

    @Inject
    lateinit var duty: RiderDuty

    @Inject
    lateinit var session: SessionStateProvider

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val cadence = LocationCadence(clock = { SystemClock.elapsedRealtime() })
    private val pings = Channel<LocationFix>(Channel.CONFLATED)
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
            // The flow never starts the service without the grant; this is the
            // belt to its braces (a revoke between the grant and this line).
            duty.report(DutyStatus.Offline(OfflineReason.PERMISSION_REVOKED))
            stopSelf()
            return
        }
        running = true
        client = LocationServices.getFusedLocationProviderClient(this)
        duty.report(DutyStatus.Online(lastPingOk = null))
        perform(cadence.start(onJob = duty.onJob.value))

        scope.launch {
            for (fix in pings) {
                val ok = rider.postLocation(fix.toRequest()) is FoodResult.Success
                if (running) duty.report(DutyStatus.Online(lastPingOk = ok))
            }
        }
        scope.launch { duty.onJob.collect { perform(cadence.onJobChanged(it)) } }
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
                is CadenceEffect.Ping -> pings.trySend(effect.fix)
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
            // No platform distance filter: the cadence applies it to pings, so a
            // stationary rider still produces fixes for the no-fix watchdog.
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
                withContext(NonCancellable) { rider.setAvailability(online = false) }
            }
            ServiceCompat.stopForeground(this@RiderLocationService, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    private fun hasLocationPermission(): Boolean =
        ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
            ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun onDutyNotification() = NotificationCompat.Builder(this, NotificationChannelSpec.RIDER_ON_DUTY.id)
        .setSmallIcon(R.drawable.ic_stat_on_duty)
        .setContentTitle("You're online")
        .setContentText("Sharing your location while you're on duty")
        .setOngoing(true)
        .setOnlyAlertOnce(true)
        .setCategory(NotificationCompat.CATEGORY_SERVICE)
        .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
        .apply {
            packageManager.getLaunchIntentForPackage(packageName)?.let { launch ->
                setContentIntent(
                    PendingIntent.getActivity(this@RiderLocationService, 0, launch, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT),
                )
            }
        }
        .build()

    companion object {
        private const val ACTION_STOP = "com.us.android.feature.rider.location.STOP"
        private const val NOTIFICATION_ID = 4_101
        private const val TICK_MILLIS = 1_000L

        /** Call only after the disclosure, the grant and the server's yes. */
        fun start(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, RiderLocationService::class.java))
        }
    }
}

/** A platform fix as the cadence reads it: bearing and accuracy only when the fix has them. */
internal fun Location.toFix(): LocationFix = LocationFix(
    latitude = latitude,
    longitude = longitude,
    accuracyMeters = if (hasAccuracy()) accuracy.toDouble() else null,
    headingDegrees = if (hasBearing()) bearing.toDouble() else null,
    atMillis = elapsedRealtimeNanos / NANOS_PER_MILLI,
)

private const val NANOS_PER_MILLI = 1_000_000L
