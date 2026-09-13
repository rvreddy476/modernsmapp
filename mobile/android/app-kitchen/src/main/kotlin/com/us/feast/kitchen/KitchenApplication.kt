package com.us.feast.kitchen

import android.annotation.SuppressLint
import android.app.Application
import android.content.Context
import android.util.Log
import com.us.android.core.notifications.NotificationChannelSpec
import dagger.hilt.android.HiltAndroidApp

/**
 * Feast Kitchen's application and Hilt root.
 *
 * Registers ONLY the kitchen's notification channels (food_orders,
 * kitchen_new_order) — never Momentum's — and reports once whether this build
 * has Firebase at all.
 */
@HiltAndroidApp
class KitchenApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        NotificationChannelSpec.createAll(this, NotificationChannelSpec.KITCHEN)
        FirebaseState.logOnce(this)
    }
}

/**
 * Whether this build carries a Firebase configuration.
 *
 * There is no google-services.json for Feast Kitchen yet, so the Google Services
 * plugin is not applied (see build.gradle.kts), `google_app_id` is absent,
 * FirebaseApp never initialises and FCM is disabled. Nothing in the kitchen
 * calls FirebaseMessaging; this only says so, once per process.
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
            Log.i(TAG, "FCM disabled: this build has no google-services.json. New-order alerts come from the in-app order queue.")
        }
    }

    private const val TAG = "FeastKitchen"
}
