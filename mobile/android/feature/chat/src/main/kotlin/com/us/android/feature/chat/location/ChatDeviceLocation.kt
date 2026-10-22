package com.us.android.feature.chat.location

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
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import java.util.Locale
import javax.inject.Inject
import kotlin.coroutines.resume

/** One device fix: where, and how sure. Never logged. */
data class DeviceFix(val latitude: Double, val longitude: Double, val accuracyM: Double?)

/** A one-shot device fix. A port so the share flow is testable without Play services. */
interface ChatLocationSource {
    fun hasPermission(): Boolean

    suspend fun current(): DeviceFix?
}

/** Address for a fix, through the PLATFORM geocoder (no third-party key); null when the device has none. */
interface ChatAddressLookup {
    suspend fun reverse(fix: DeviceFix): String?
}

/** Fused location: ONE current fix at high accuracy, cancelled with the caller. Never watched. */
class FusedChatLocationSource @Inject constructor(
    @ApplicationContext private val context: Context,
) : ChatLocationSource {

    override fun hasPermission(): Boolean =
        granted(Manifest.permission.ACCESS_FINE_LOCATION) || granted(Manifest.permission.ACCESS_COARSE_LOCATION)

    @SuppressLint("MissingPermission") // checked on the line below
    override suspend fun current(): DeviceFix? {
        if (!hasPermission()) return null
        val client = LocationServices.getFusedLocationProviderClient(context)
        val cancellation = CancellationTokenSource()
        return suspendCancellableCoroutine { continuation ->
            continuation.invokeOnCancellation { cancellation.cancel() }
            client.getCurrentLocation(Priority.PRIORITY_HIGH_ACCURACY, cancellation.token)
                .addOnSuccessListener { location ->
                    if (continuation.isActive) {
                        continuation.resume(
                            location?.let {
                                DeviceFix(it.latitude, it.longitude, if (it.hasAccuracy()) it.accuracy.toDouble() else null)
                            },
                        )
                    }
                }
                .addOnFailureListener { if (continuation.isActive) continuation.resume(null) }
                .addOnCanceledListener { if (continuation.isActive) continuation.resume(null) }
        }
    }

    private fun granted(permission: String): Boolean =
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
}

class AndroidChatAddressLookup @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ChatAddressLookup {

    private val geocoder by lazy { Geocoder(context, Locale.getDefault()) }

    @Suppress("DEPRECATION", "TooGenericExceptionCaught", "SwallowedException")
    override suspend fun reverse(fix: DeviceFix): String? = withContext(io) {
        if (!Geocoder.isPresent()) return@withContext null
        val address = try {
            geocoder.getFromLocation(fix.latitude, fix.longitude, 1)?.firstOrNull()
        } catch (e: Exception) {
            // Never the exception text: it can carry the coordinates.
            null
        } ?: return@withContext null
        address.getAddressLine(0)?.takeIf { it.isNotBlank() }
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class ChatLocationModule {

    @Binds
    abstract fun bindChatLocationSource(impl: FusedChatLocationSource): ChatLocationSource

    @Binds
    abstract fun bindChatAddressLookup(impl: AndroidChatAddressLookup): ChatAddressLookup
}
