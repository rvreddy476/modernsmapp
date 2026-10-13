package com.us.doorstep.pro

import android.annotation.SuppressLint
import android.app.Application
import android.content.Context
import android.util.Log
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.doorstep.pro.push.ProPushRegistration
import dagger.hilt.android.HiltAndroidApp
import javax.inject.Inject

/**
 * Unified Partner application and Hilt root; retains the installed package identity.
 *
 * Registers delivery, home-service and Moped channels, starts push-token registration,
 * and reports once whether this build has Firebase configured.
 */
@HiltAndroidApp
class DoorstepProApplication : Application() {

    @Inject
    lateinit var pushRegistration: ProPushRegistration

    override fun onCreate() {
        super.onCreate()
        NotificationChannelSpec.createAll(this, NotificationChannelSpec.DOORSTEP_PRO +
            NotificationChannelSpec.RIDER + NotificationChannelSpec.CAPTAIN + NotificationChannelSpec.COMMERCE_DELIVERY)
        FirebaseState.logOnce(this)
        pushRegistration.start()
    }
}

/**
 * Whether this build carries a matching Firebase configuration. Builds without
 * the local google-services.json use the in-app realtime stream and polling only.
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
            Log.i(TAG, "FCM disabled: this build has no google-services.json. Offers come from the in-app stream.")
        }
    }

    private const val TAG = "DoorstepPro"
}
