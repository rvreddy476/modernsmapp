package com.us.android.feature.rider.offers

import android.Manifest
import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.rider.R
import dagger.hilt.android.qualifiers.ApplicationContext
import java.time.Duration
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/** One offer to alert: its id and how long it has left on the server's clock. */
data class OfferAlert(val offerId: String, val remaining: Duration)

/** What to change in the tray after a feed refresh. */
data class OfferAlertChanges(val post: List<OfferAlert>, val cancel: Set<String>)

/**
 * Which offers get a heads-up and which are taken down (food audit H1). Pure.
 *
 * A new, unexpired offer id is alerted once. An id that has left the pending
 * list (accepted, declined, taken, expired) is cancelled. An offer already
 * alerted is never alerted again, so a refresh every few seconds does not
 * re-ring.
 */
object OfferAlertPlan {

    fun diff(alerted: Set<String>, offers: List<DeliveryOfferDto>, serverNow: Instant): OfferAlertChanges {
        val live = offers.mapNotNull { offer ->
            val expiresAt = ServerTime.parse(offer.expiresAt) ?: return@mapNotNull null
            val remaining = Duration.between(serverNow, expiresAt)
            if (remaining > Duration.ZERO) OfferAlert(offer.id, remaining) else null
        }
        val liveIds = live.map { it.offerId }.toSet()
        return OfferAlertChanges(
            post = live.filter { it.offerId !in alerted },
            cancel = alerted - liveIds,
        )
    }
}

/**
 * Posts the rider's job-offer heads-up on `rider_job_offer`, for as long as
 * the offer lives, and takes it down when the offer leaves the list.
 *
 * Same notification id as the FCM presenter's for the same offer (the offer id's
 * hash), so a push and the stream collapse into one card, and the same sound
 * claim, so only one of them rings. The tap opens the app at
 * `/rider/offers/{id}`; nothing on the card accepts by itself.
 *
 * Main thread only (the location service's scope).
 */
@Singleton
class RiderOfferAlerts @Inject constructor(
    @ApplicationContext private val context: Context,
    private val sounds: DoorstepOfferAlertCoordinator,
) {
    private val alerted = mutableSetOf<String>()

    fun update(offers: List<DeliveryOfferDto>, serverNow: Instant) {
        val changes = OfferAlertPlan.diff(alerted, offers, serverNow)
        changes.cancel.forEach(::cancel)
        changes.post.forEach(::post)
    }

    /** Off duty: no offer can be taken, so none stays in the tray. */
    fun clearAll() {
        alerted.toList().forEach(::cancel)
    }

    private fun cancel(offerId: String) {
        NotificationManagerCompat.from(context).cancel(offerId.hashCode())
        alerted -= offerId
    }

    @SuppressLint("MissingPermission") // checked in canPost()
    private fun post(alert: OfferAlert) {
        alerted += alert.offerId
        if (!canPost()) return
        val remainingMillis = alert.remaining.toMillis()
        val silent = !sounds.claimSound(alert.offerId, remainingMillis)
        val notification = NotificationCompat.Builder(context, NotificationChannelSpec.RIDER_JOB_OFFER.id)
            .setSmallIcon(R.drawable.ic_stat_on_duty)
            .setContentTitle("New delivery job")
            .setContentText("Open Feast Rider to accept or decline")
            .setCategory(NotificationCompat.CATEGORY_EVENT)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setAutoCancel(true)
            .setOnlyAlertOnce(true)
            .setSilent(silent)
            .setTimeoutAfter(remainingMillis)
            .setWhen(System.currentTimeMillis() + remainingMillis)
            .setShowWhen(true)
            .setUsesChronometer(true)
            .setChronometerCountDown(true)
            .apply { openOffer(alert.offerId)?.let(::setContentIntent) }
            .build()
        NotificationManagerCompat.from(context).notify(alert.offerId.hashCode(), notification)
    }

    private fun openOffer(offerId: String): PendingIntent? {
        val launch = context.packageManager.getLaunchIntentForPackage(context.packageName) ?: return null
        launch.putExtra(EXTRA_TYPE, OFFER_TYPE)
        launch.putExtra(EXTRA_OFFER_ID, offerId)
        launch.putExtra(EXTRA_DEEP_LINK, "/rider/offers/$offerId")
        return PendingIntent.getActivity(
            context,
            offerId.hashCode(),
            launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    private fun canPost(): Boolean {
        if (!NotificationManagerCompat.from(context).areNotificationsEnabled()) return false
        return Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
    }

    private companion object {
        // The keys RiderActivity reads from a push intent (the presenter writes the same ones).
        const val EXTRA_TYPE = "type"
        const val EXTRA_OFFER_ID = "offer_id"
        const val EXTRA_DEEP_LINK = "deep_link"
        const val OFFER_TYPE = "food_delivery_offer"
    }
}
