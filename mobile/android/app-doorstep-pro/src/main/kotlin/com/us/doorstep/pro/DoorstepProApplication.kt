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
 * Doorstep Pro's application and Hilt root.
 *
 * Registers ONLY the professional's notification channels (doorstep_pro_offers,
 * _jobs, _account, _earnings and the on-duty one) — never Momentum's, the Feast
 * partners' or the captain's — starts push-token registration, and reports once
 * whether this build has Firebase at all.
 */
@HiltAndroidApp
class DoorstepProApplication : Application() {

    @Inject
    lateinit var pushRegistration: ProPushRegistration

    override fun onCreate() {
        super.onCreate()
        NotificationChannelSpec.createAll(this, NotificationChannelSpec.DOORSTEP_PRO)
        FirebaseState.logOnce(this)
        pushRegistration.start()
    }
}

/**
 * Whether this build carries a Firebase configuration. There is no
 * google-services.json for Doorstep Pro yet, so FCM is disabled and offers
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
            Log.i(TAG, "FCM disabled: this build has no google-services.json. Offers come from the in-app stream.")
        }
    }

    private const val TAG = "DoorstepPro"
}
