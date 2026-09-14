package com.us.android.feature.feast.address

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
import java.util.Locale
import javax.inject.Inject
import kotlin.coroutines.resume

data class Coordinates(val latitude: Double, val longitude: Double)

/** A one-shot device fix. A port so the address step is testable without Play services. */
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
        return suspendCancellableCoroutine { continuation ->
            continuation.invokeOnCancellation { cancellation.cancel() }
            client.getCurrentLocation(Priority.PRIORITY_HIGH_ACCURACY, cancellation.token)
                .addOnSuccessListener { location ->
                    if (continuation.isActive) continuation.resume(location?.let { Coordinates(it.latitude, it.longitude) })
                }
                .addOnFailureListener { if (continuation.isActive) continuation.resume(null) }
                .addOnCanceledListener { if (continuation.isActive) continuation.resume(null) }
        }
    }

    private fun granted(permission: String): Boolean =
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
}

data class LookedUpAddress(
    val line1: String,
    val locality: String,
    val city: String,
    val state: String,
    val postalCode: String,
)

/**
 * Address ⇄ coordinates without Places, through the PLATFORM geocoder: reverse
 * after a fix pre-fills the form; forward pins a typed address for a customer
 * who declined location. Both return null when the device has no backend.
 */
interface AddressLookup {
    suspend fun reverse(coordinates: Coordinates): LookedUpAddress?

    suspend fun forward(query: String): Coordinates?
}

class AndroidAddressLookup @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : AddressLookup {

    private val geocoder by lazy { Geocoder(context, Locale.Builder().setLanguage("en").setRegion("IN").build()) }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught", "SwallowedException")
    override suspend fun reverse(coordinates: Coordinates): LookedUpAddress? = withContext(io) {
        if (!Geocoder.isPresent()) return@withContext null
        val address = try {
            geocoder.getFromLocation(coordinates.latitude, coordinates.longitude, 1)?.firstOrNull()
        } catch (e: Exception) {
            null
        } ?: return@withContext null
        val line1 = listOfNotNull(address.subThoroughfare, address.thoroughfare)
            .filter { it.isNotBlank() }
            .distinct()
            .joinToString(", ")
            .ifBlank { address.getAddressLine(0).orEmpty() }
        LookedUpAddress(
            line1 = line1.take(MAX_LINE),
            locality = address.subLocality.orEmpty(),
            city = (address.locality ?: address.subAdminArea).orEmpty(),
            state = address.adminArea.orEmpty(),
            postalCode = address.postalCode.orEmpty(),
        )
    }

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

    private companion object {
        const val MAX_LINE = 255
    }
}
