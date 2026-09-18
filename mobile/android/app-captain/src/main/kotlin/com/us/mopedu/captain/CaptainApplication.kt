package com.us.mopedu.captain

import android.annotation.SuppressLint
import android.app.Application
import android.content.Context
import android.util.Log
import com.us.android.core.notifications.NotificationChannelSpec
import dagger.hilt.android.HiltAndroidApp

/**
 * Mopedu Captain's application and Hilt root.
 *
 * Registers ONLY the captain's notification channels (captain_offer,
 * captain_on_duty, captain_earnings, captain_account) — never Momentum's or
 * Feast's — and reports once whether this build has Firebase at all.
 */
@HiltAndroidApp
class CaptainApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        NotificationChannelSpec.createAll(this, NotificationChannelSpec.CAPTAIN)
        FirebaseState.logOnce(this)
    }
}

/**
 * Whether this build carries a Firebase configuration. There is no
 * google-services.json for Mopedu Captain yet, so FCM is disabled and offers
 * arrive by polling while the app is open.
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
            Log.i(TAG, "FCM disabled: this build has no google-services.json. Offers come from polling while the app is open.")
        }
    }

    private const val TAG = "MopeduCaptain"
}
