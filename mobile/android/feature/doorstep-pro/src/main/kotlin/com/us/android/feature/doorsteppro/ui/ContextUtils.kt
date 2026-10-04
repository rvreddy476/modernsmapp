package com.us.android.feature.doorsteppro.ui

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.ContextCompat
import java.net.URLEncoder

internal fun Context.findActivity(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}

internal fun Context.locationGranted(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
        ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

internal fun Context.notificationsGranted(): Boolean =
    Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
        ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

internal fun Context.openAppSettings() {
    startSafely(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", packageName, null)))
}

/**
 * A maps hand-off for the customer's address (Feast Rider's NavIntent idea):
 * a `geo:` pin from the coordinates with the locality as its label. No
 * server link exists for Doorstep, and nothing from a payload is handed to
 * ACTION_VIEW but two numbers and an encoded label.
 */
object NavHandOff {
    private const val MAX_LAT = 90.0
    private const val MAX_LNG = 180.0

    fun geoUri(lat: Double, lng: Double, label: String?): String? {
        if (lat == 0.0 && lng == 0.0) return null
        if (lat !in -MAX_LAT..MAX_LAT || lng !in -MAX_LNG..MAX_LNG) return null
        val suffix = label?.takeIf { it.isNotBlank() }?.let { "(${URLEncoder.encode(it, "UTF-8")})" }.orEmpty()
        return "geo:$lat,$lng?q=$lat,$lng$suffix"
    }
}

/** Opens a maps hand-off. False when nothing on the phone can open it; never throws. */
internal fun Context.openNavigation(uri: String): Boolean = startSafely(Intent(Intent.ACTION_VIEW, Uri.parse(uri)))

private fun Context.startSafely(intent: Intent): Boolean = try {
    startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    true
} catch (e: ActivityNotFoundException) {
    false
} catch (e: SecurityException) {
    false
}
