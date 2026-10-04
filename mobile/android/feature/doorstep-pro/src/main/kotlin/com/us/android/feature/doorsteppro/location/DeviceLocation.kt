package com.us.android.feature.doorsteppro.location

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
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.util.Locale
import javax.inject.Inject
import kotlin.coroutines.resume

/*
 * One-shot device location and the platform geocoder. COPIED from
 * :feature:doorstep's address/DeviceLocation.kt (features may not share code).
 */

data class Coordinates(val latitude: Double, val longitude: Double)

/** A one-shot device fix. A port so the screens test without Play services. */
interface CurrentLocationSource {
    fun hasPermission(): Boolean

    suspend fun current(): Coordinates?
}

/** Fused location (play-services-location 21.2.0, the version in the offline cache). */
class FusedCurrentLocationSource @Inject constructor(
    @ApplicationContext private val context: Context,
) : CurrentLocationSource {

    override fun hasPermission(): Boolean =
        granted(Manifest.permission.ACCESS_FINE_LOCATION) || granted(Manifest.permission.ACCESS_COARSE_LOCATION)

    @SuppressLint("MissingPermission") // checked on the line below
    override suspend fun current(): Coordinates? {
        if (!hasPermission()) return null
        val client = LocationServices.getFusedLocationProviderClient(context)
        val cancellation = CancellationTokenSource()
        return withTimeoutOrNull(FIX_TIMEOUT_MILLIS) {
            suspendCancellableCoroutine { continuation ->
                continuation.invokeOnCancellation { cancellation.cancel() }
                client.getCurrentLocation(Priority.PRIORITY_HIGH_ACCURACY, cancellation.token)
                    .addOnSuccessListener { location ->
                        if (continuation.isActive) continuation.resume(location?.let { Coordinates(it.latitude, it.longitude) })
                    }
                    .addOnFailureListener { if (continuation.isActive) continuation.resume(null) }
                    .addOnCanceledListener { if (continuation.isActive) continuation.resume(null) }
            }
        }
    }

    private fun granted(permission: String): Boolean =
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED

    private companion object {
        const val FIX_TIMEOUT_MILLIS = 20_000L
    }
}

/**
 * A typed place → coordinates through the PLATFORM geocoder, for a
 * professional who declined location and types their area instead. Null
 * when the device has no geocoder backend.
 */
interface PlaceLookup {
    suspend fun forward(query: String): Coordinates?
}

class AndroidPlaceLookup @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : PlaceLookup {

    private val geocoder by lazy { Geocoder(context, Locale.Builder().setLanguage("en").setRegion("IN").build()) }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught", "SwallowedException")
    override suspend fun forward(query: String): Coordinates? = withContext(io) {
        if (!Geocoder.isPresent() || query.isBlank()) return@withContext null
        val address = try {
            geocoder.getFromLocationName(query, 1)?.firstOrNull()
        } catch (e: Exception) {
            null
        } ?: return@withContext null
        if (address.hasLatitude() && address.hasLongitude()) Coordinates(address.latitude, address.longitude) else null
    }
}
