package com.us.android.feature.doorsteppro.location

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
import com.us.android.feature.doorsteppro.R
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
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
 * On duty: a foreground service of type `location` that feeds the
 * professional's position to `POST /v1/doorstep/pro/location` on
 * [ProCadence]'s rules (Feast Rider's RiderLocationService, copied).
 *
 * Started ONLY by Home after the prominent disclosure, the foreground location
 * grant and the server accepting `duty/on` (see [GoOnDutyFlow]). It never asks
 * for background location: a location foreground service keeps while-in-use
 * access for as long as it runs, and its ongoing notification on
 * `doorstep_pro_on_duty` is the professional's constant signal that location
 * is being shared.
 *
 * A 409 DOORSTEP_NOT_ON_DUTY on a ping means the server took the professional
 * off duty (ops, suspension, a restart): the service stops rather than keep
 * sending pings the server refuses.
 */
@AndroidEntryPoint
class ProLocationService : Service() {

    @Inject
    lateinit var repository: DoorstepProRepository

    @Inject
    lateinit var duty: ProDuty

    @Inject
    lateinit var session: SessionStateProvider

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val cadence = ProCadence(clock = { SystemClock.elapsedRealtime() })
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
            duty.report(DutyStatus.Offline(OfflineReason.PERMISSION_REVOKED))
            stopSelf()
            return
        }
        running = true
        client = LocationServices.getFusedLocationProviderClient(this)
        duty.report(DutyStatus.Online(lastPingOk = null))
        perform(cadence.start(onJob = duty.travelling.value))

        scope.launch {
            for (fix in pings) {
                val result = repository.postLocation(fix.toRequest())
                if (result is ProResult.Failure && result.error.code == ProCodes.NOT_ON_DUTY) {
                    perform(cadence.onServerRefused())
                } else if (running) {
                    duty.report(DutyStatus.Online(lastPingOk = result is ProResult.Success))
                }
            }
        }
        scope.launch { duty.travelling.collect { perform(cadence.onJobChanged(it)) } }
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
            // stationary professional still produces fixes for the no-fix watchdog.
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
            // After the server refused a ping it already holds us off duty.
            if (reason != OfflineReason.SIGNED_OUT && reason != OfflineReason.SERVER_REFUSED) {
                withContext(NonCancellable) { repository.dutyOff() }
            }
            ServiceCompat.stopForeground(this@ProLocationService, ServiceCompat.STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    private fun hasLocationPermission(): Boolean =
        ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
            ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun onDutyNotification() = NotificationCompat.Builder(this, NotificationChannelSpec.DOORSTEP_PRO_ON_DUTY.id)
        .setSmallIcon(R.drawable.ic_stat_doorstep_pro_on_duty)
        .setContentTitle("You're on duty")
        .setContentText("Sharing your location while you're on duty")
        .setOngoing(true)
        .setOnlyAlertOnce(true)
        .setCategory(NotificationCompat.CATEGORY_SERVICE)
        .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
        .apply {
            packageManager.getLaunchIntentForPackage(packageName)?.let { launch ->
                setContentIntent(
                    PendingIntent.getActivity(this@ProLocationService, 0, launch, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT),
                )
            }
        }
        .build()

    companion object {
        private const val ACTION_STOP = "com.us.android.feature.doorsteppro.location.STOP"
        private const val NOTIFICATION_ID = 4_301
        private const val TICK_MILLIS = 1_000L

        /** Call only after the disclosure, the grant and the server's yes. */
        fun start(context: Context) {
            ContextCompat.startForegroundService(context, Intent(context, ProLocationService::class.java))
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
