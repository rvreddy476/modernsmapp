package com.us.android.feature.mopedu.rider.location

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.location.Geocoder
import androidx.core.content.ContextCompat
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority
import com.google.android.gms.tasks.CancellationTokenSource
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.mobility.model.GeoPoint
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import java.util.Locale
import javax.inject.Inject
import kotlin.coroutines.resume

sealed interface LocationStep {
    data object Idle : LocationStep

    /** The in-app explanation is on screen. The system prompt has NOT been shown. */
    data object ExplainingPermission : LocationStep

    /** The system prompt is up. */
    data object AwaitingPermission : LocationStep

    data object Locating : LocationStep

    data class Located(val point: GeoPoint) : LocationStep

    /** Refused. [canAskAgain] false means only Settings can grant it now. Typing an address stays open. */
    data class Denied(val canAskAgain: Boolean) : LocationStep

    /** Permission granted but no fix came back. Typing an address stays open. */
    data object Unavailable : LocationStep
}

/** What the screen must do after a transition. */
sealed interface LocationEffect {
    data object None : LocationEffect

    data object RequestPermission : LocationEffect

    data object FetchLocation : LocationEffect
}

/**
 * "Use my location" with the rationale ALWAYS before the system prompt.
 *
 * Copied from Dating's `location/DatingLocation.kt` (features may not share
 * code): the only path to [LocationEffect.RequestPermission] runs through
 * [LocationStep.ExplainingPermission] and the person choosing to continue;
 * out-of-order calls are ignored.
 */
class LocationPermissionFlow {

    var step: LocationStep = LocationStep.Idle
        private set

    fun onUseCurrentLocation(permissionGranted: Boolean): LocationEffect =
        if (permissionGranted) {
            step = LocationStep.Locating
            LocationEffect.FetchLocation
        } else {
            step = LocationStep.ExplainingPermission
            LocationEffect.None
        }

    fun onRationaleAccepted(): LocationEffect {
        if (step != LocationStep.ExplainingPermission) return LocationEffect.None
        step = LocationStep.AwaitingPermission
        return LocationEffect.RequestPermission
    }

    fun onRationaleDismissed() {
        if (step == LocationStep.ExplainingPermission) step = LocationStep.Idle
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean): LocationEffect {
        if (step != LocationStep.AwaitingPermission) return LocationEffect.None
        return if (granted) {
            step = LocationStep.Locating
            LocationEffect.FetchLocation
        } else {
            step = LocationStep.Denied(canAskAgain)
            LocationEffect.None
        }
    }

    fun onLocationResult(point: GeoPoint?) {
        if (step != LocationStep.Locating) return
        step = if (point != null) LocationStep.Located(point) else LocationStep.Unavailable
    }
}

/** A one-shot device fix, and the platform geocoder both ways. A port so the screens test without Play services. */
interface CurrentLocationSource {
    fun hasPermission(): Boolean

    /** The device's position, reverse-geocoded to a street address when the device can. */
    suspend fun current(): GeoPoint?

    /** A typed address as a point, through the platform geocoder; null when it finds nothing. */
    suspend fun geocode(query: String): GeoPoint?
}

/** Fused location at high accuracy: the pickup point is where the captain drives to. */
class FusedCurrentLocationSource @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : CurrentLocationSource {

    private val geocoder by lazy { Geocoder(context, Locale.Builder().setLanguage("en").setRegion("IN").build()) }

    override fun hasPermission(): Boolean =
        granted(Manifest.permission.ACCESS_FINE_LOCATION) || granted(Manifest.permission.ACCESS_COARSE_LOCATION)

    @SuppressLint("MissingPermission") // checked on the line below
    override suspend fun current(): GeoPoint? {
        if (!hasPermission()) return null
        val client = LocationServices.getFusedLocationProviderClient(context)
        val cancellation = CancellationTokenSource()
        val fix = suspendCancellableCoroutine<GeoPoint?> { continuation ->
            continuation.invokeOnCancellation { cancellation.cancel() }
            client.getCurrentLocation(Priority.PRIORITY_HIGH_ACCURACY, cancellation.token)
                .addOnSuccessListener { location ->
                    if (continuation.isActive) continuation.resume(location?.let { GeoPoint(it.latitude, it.longitude) })
                }
                .addOnFailureListener { if (continuation.isActive) continuation.resume(null) }
                .addOnCanceledListener { if (continuation.isActive) continuation.resume(null) }
        } ?: return null
        return fix.copy(address = addressOf(fix) ?: "Current location", label = "Current location")
    }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught", "SwallowedException")
    override suspend fun geocode(query: String): GeoPoint? = withContext(io) {
        if (!Geocoder.isPresent() || query.isBlank()) return@withContext null
        val address = try {
            geocoder.getFromLocationName(query.trim(), 1)?.firstOrNull()
        } catch (e: Exception) {
            null
        } ?: return@withContext null
        GeoPoint(
            lat = address.latitude,
            lng = address.longitude,
            address = address.getAddressLine(0)?.takeIf { it.isNotBlank() } ?: query.trim(),
            label = query.trim(),
        )
    }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught", "SwallowedException")
    private suspend fun addressOf(point: GeoPoint): String? = withContext(io) {
        if (!Geocoder.isPresent()) return@withContext null
        val address = try {
            geocoder.getFromLocation(point.lat, point.lng, 1)?.firstOrNull()
        } catch (e: Exception) {
            null
        }
        address?.getAddressLine(0)?.takeIf { it.isNotBlank() }
    }

    private fun granted(permission: String): Boolean =
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
}
