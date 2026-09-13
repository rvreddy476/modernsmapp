package com.us.feast.rider

import android.annotation.SuppressLint
import android.app.Application
import android.content.Context
import android.util.Log
import com.us.android.core.notifications.NotificationChannelSpec
import dagger.hilt.android.HiltAndroidApp

/**
 * Feast Rider's application and Hilt root.
 *
 * Registers ONLY the rider's notification channels (food_orders,
 * rider_job_offer, rider_on_duty) — never Momentum's or the kitchen's — and
 * reports once whether this build has Firebase at all.
 */
@HiltAndroidApp
class RiderApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        NotificationChannelSpec.createAll(this, NotificationChannelSpec.RIDER)
        FirebaseState.logOnce(this)
    }
}

/**
 * Whether this build carries a Firebase configuration. There is no
 * google-services.json for Feast Rider yet, so FCM is disabled and job offers
 * arrive through the in-app realtime stream (and its polling fallback) only.
 */
internal object FirebaseState {

    @Volatile
    private var logged = false

    @SuppressLint("DiscouragedApi") // checking for a generated resource by name is the point
    fun isConfigured(context: Context): Boolean =
        context.resources.getIdentifier("google_app_id", "string", context.packageName) != 0

    fun logOnce(context: Context) {
        if (logged) return
        logged = true
        if (!isConfigured(context)) {
            Log.i(TAG, "FCM disabled: this build has no google-services.json. Job offers come from the in-app stream.")
        }
    }

    private const val TAG = "FeastRider"
}
