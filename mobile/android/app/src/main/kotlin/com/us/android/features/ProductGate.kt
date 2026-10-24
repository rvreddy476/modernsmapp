package com.us.android.features

import com.us.android.core.model.CustomerNotification
import com.us.android.core.model.Product
import com.us.android.core.model.productOf
import com.us.android.explore.LauncherApp
import com.us.android.push.PushDestination
import com.us.android.push.PushDestinations

/**
 * Which parked product (founder decision C, 24 Oct 2026; `GET /v1/features`)
 * each way into the app belongs to. `:app` owns this because only it knows
 * every launcher tile and every push type. Pure, so the tables are tests.
 */

/** The product behind a launcher tile; null for a tile no gateway switch closes. */
val LauncherApp.product: Product?
    get() = when (this) {
        LauncherApp.ASK -> Product.QA
        LauncherApp.FEAST -> Product.FOOD
        LauncherApp.RIDE -> Product.RIDER
        LauncherApp.MATCH -> Product.DATING
        LauncherApp.DOORSTEP -> Product.DOORSTEP
        LauncherApp.CHAT, LauncherApp.FRIENDS, LauncherApp.ALERTS, LauncherApp.LIVE,
        LauncherApp.MSTORE, LauncherApp.MSELLER, LauncherApp.TUBE,
        -> null
    }

/** The product a notification tap or app link lands in; null for one that belongs to none. */
fun productOfPush(destination: PushDestination): Product? {
    CustomerNotification.service(destination.type)?.let(::productOf)?.let { return it }
    return when (destination.type) {
        PushDestinations.TYPE_DATING_SPARK,
        PushDestinations.TYPE_DATING_MATCH,
        PushDestinations.TYPE_DATING_MESSAGE,
        PushDestinations.TYPE_DATING_FIRST_MESSAGE,
        PushDestinations.TYPE_DATING_SCAM_ALERT,
        PushDestinations.TYPE_DATING_DATE_CHECKIN,
        -> Product.DATING
        in PushDestinations.RIDE_TYPES -> Product.RIDER
        in PushDestinations.DOORSTEP_TYPES -> Product.DOORSTEP
        else -> null
    }
}
