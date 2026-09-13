package com.us.android.feature.kitchen.location

import android.content.Context
import android.location.Geocoder
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.feature.kitchen.kyc.GstStateCodes
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import java.util.Locale
import javax.inject.Inject

data class LookedUpAddress(
    val line1: String,
    val city: String,
    /** A name food-service accepts for `state`, or empty. */
    val state: String,
    val postalCode: String,
)

/**
 * Address ⇄ coordinates without Places.
 *
 * Places autocomplete is not in the offline cache, so this uses the PLATFORM
 * geocoder (android.location.Geocoder — no Play library): reverse after a
 * location fix pre-fills the address, and forward turns a typed address into
 * the pin food-service requires when location permission was declined. Both
 * return null when the device has no geocoder backend; the partner can still
 * use "Use my current location".
 */
interface AddressLookup {
    suspend fun reverse(coordinates: Coordinates): LookedUpAddress?

    suspend fun forward(query: String): Coordinates?
}

class AndroidAddressLookup @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : AddressLookup {

    private val geocoder by lazy { Geocoder(context, INDIA_ENGLISH) }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught") // the listener API is 33+; this is blocking on IO
    override suspend fun reverse(coordinates: Coordinates): LookedUpAddress? = withContext(io) {
        if (!Geocoder.isPresent()) return@withContext null
        val address = try {
            geocoder.getFromLocation(coordinates.latitude, coordinates.longitude, 1)?.firstOrNull()
        } catch (e: Exception) {
            null
        } ?: return@withContext null
        val line1 = listOfNotNull(address.subThoroughfare, address.thoroughfare, address.subLocality)
            .filter { it.isNotBlank() }
            .distinct()
            .joinToString(", ")
            .ifBlank { address.getAddressLine(0).orEmpty() }
        LookedUpAddress(
            line1 = line1.take(MAX_LINE),
            city = (address.locality ?: address.subAdminArea).orEmpty(),
            state = matchStateName(address.adminArea),
            postalCode = address.postalCode.orEmpty(),
        )
    }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught")
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
        val INDIA_ENGLISH: Locale = Locale.Builder().setLanguage("en").setRegion("IN").build()
        const val MAX_LINE = 255
    }
}

/** The server's state name for a geocoder admin area, or "" when it is not one. */
internal fun matchStateName(adminArea: String?): String {
    val area = adminArea?.trim().orEmpty()
    if (area.isEmpty()) return ""
    return GstStateCodes.locationStateNames.firstOrNull { it.equals(area, ignoreCase = true) }.orEmpty()
}
