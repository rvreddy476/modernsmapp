package com.us.android.core.notifications

import android.Manifest
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.Uri
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton
import com.us.android.core.notifications.data.PushApp
import java.util.Optional

/**
 * Renders an incoming data push as a system notification.
 *
 * Data-only pushes never render themselves, which is the whole reason they are
 * used: the app gets to decide. That decision belongs here rather than in the
 * messaging service so it can be unit-tested without a live FCM callback.
 *
 * ## PRIVACY POSTURE FOR CHAT
 *
 * Chat pushes are generic BY CONSTRUCTION: notification-service sends
 * "New Message" with no message text, no sender name and no preview
 * (`notifTitleBody`, notification.go). This presenter therefore cannot leak
 * message content or enumerate senders on a lock screen even by mistake —
 * there is nothing to leak in the payload. The message-preview privacy
 * setting is honoured trivially, and a LOCKED chat's pushes carry nothing the
 * lock protects.
 */
@Singleton
class NotificationPresenter @Inject constructor(
    @ApplicationContext private val context: Context,
    private val foreground: AppForegroundState,
    private val offerAlerts: DoorstepOfferAlertCoordinator,
    private val installedApp: Optional<PushApp> = Optional.empty(),
    private val inAppOffers: PartnerOfferInbox = PartnerOfferInbox(),
) {
    init { inAppOffers.attachStorage(context) }

    fun present(data: Map<String, String>) {
        if (!acceptsInstalledAppNotification(installedApp.orElse(PushApp.MOMENTUM), data[KEY_TYPE])) return
        val offer = DoorstepOfferNotification.evaluate(data)
        if (installedApp.orElse(PushApp.MOMENTUM) == PushApp.PARTNER) {
            if (offer is DoorstepOfferNotification.Open && inAppOffers.isBlocked(data["test_only"] == "true")) {
                cancelForSubject(offer.id)
                return
            }
            inAppOffers.receive(data)
            if (offer is DoorstepOfferNotification.Open && foreground.isForeground && inAppOffers.visibleHost) {
                cancelForSubject(offer.id)
                return
            }
        }
        if (offer is DoorstepOfferNotification.Expired) {
            cancelForSubject(offer.id)
            return
        }
        if (offer is DoorstepOfferNotification.Invalid) return
        val title = data[KEY_TITLE] ?: return
        val body = data[KEY_BODY].orEmpty()
        val type = data[KEY_TYPE]
        // Chat parity: a chat push rides its conversation's alert-tone channel (CONTRACT §8).
        val channel = NotificationChannelSpec.forPush(type, data[KEY_ALERT_TONE])
        val partnerCategory = PartnerOfferCategory.forType(type)
        val actionable = installedApp.orElse(PushApp.MOMENTUM) == PushApp.PARTNER &&
            partnerCategory != null && offer is DoorstepOfferNotification.Open &&
            runCatching { java.util.UUID.fromString(offer.id) }.isSuccess

        // Push/socket de-duplication, client half: while the app is visible
        // the session socket already delivered this chat message to the open
        // surface. (Background chat pushes carry a `notification` block and
        // are system-rendered without reaching this code at all.)
        if (suppressForegroundNotification(type, foreground.isForeground)) return

        if (!canPost()) return

        // Never discard an offer just because the app is open. If its request
        // card already owns the ringtone, post the same visible alert silently.
        val silentOffer = offer is DoorstepOfferNotification.Open &&
            !offerAlerts.claimSound(offer.id, offer.remainingMillis)

        val notification = NotificationCompat.Builder(context, channel.id)
            .setContentTitle(title)
            .setContentText(body)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setAutoCancel(true)
            .setPriority(channel.compatPriority())
            .apply {
                if (offer is DoorstepOfferNotification.Open) {
                    addExtras(android.os.Bundle().apply {
                        putBoolean("partner_offer", actionable)
                        putBoolean("partner_practice", data["test_only"] == "true")
                    })
                    if (silentOffer) setSilent(true)
                    setTimeoutAfter(offer.remainingMillis)
                    setOnlyAlertOnce(true)
                    setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
                    setCategory(NotificationCompat.CATEGORY_EVENT)
                    setContentText("Open the app to accept or decline")
                    partnerCategory?.takeIf { installedApp.orElse(PushApp.MOMENTUM) == PushApp.PARTNER }?.let {
                        setContentTitle("${if (data["test_only"] == "true") "TEST · " else ""}${it.label} · New request")
                        setSubText(it.label)
                    }
                    setWhen(System.currentTimeMillis() + offer.remainingMillis)
                    setShowWhen(true)
                    setUsesChronometer(true)
                    if (Build.VERSION.SDK_INT >= 24) setChronometerCountDown(true)
                    if (actionable) {
                        setContentText(if (data["test_only"] == "true") "Practice request only — no booking or payment" else "Respond before the countdown ends")
                        for (decision in listOf("accept", "decline")) {
                            addAction(NotificationCompat.Action.Builder(
                                0, if (decision == "accept") "Accept" else "Decline", offerIntent(data, decision),
                            ).setAuthenticationRequired(true).build())
                        }
                    }
                }
            }
            .apply { (if (actionable) offerIntent(data, "view") else tapIntent(data))?.let(::setContentIntent) }
            .build()

        NotificationManagerCompat.from(context).notify(notificationId(data), notification)
    }

    /**
     * Clears the notification for one subject — called when its conversation
     * is opened/read, so a handled message does not linger in the shade.
     */
    fun cancelForSubject(subjectId: String) {
        if (subjectId.isBlank()) return
        NotificationManagerCompat.from(context).cancel(subjectId.hashCode())
    }

    /** Clear only work offers of this mode, never account, safety or customer updates. */
    fun cancelPartnerOffers(testOnly: Boolean) {
        context.getSystemService(NotificationManager::class.java)?.activeNotifications?.forEach { row ->
            if(row.notification.extras.getBoolean("partner_offer") && row.notification.extras.getBoolean("partner_practice") == testOnly)
                NotificationManagerCompat.from(context).cancel(row.tag, row.id)
        }
    }

    /**
     * A tap opens the app's launcher activity carrying the push's routing
     * extras — the same keys FCM itself attaches to the launch intent for a
     * background (system-rendered) tap, so BOTH tap paths land in
     * MainActivity with an identical contract and one navigation code path.
     */
    private fun tapIntent(data: Map<String, String>): PendingIntent? {
        val launch = context.packageManager.getLaunchIntentForPackage(context.packageName)
            ?: return null
        launch.putExtra(KEY_TYPE, data[KEY_TYPE])
        // A chat push names its thread as `conversation_id` (chat parity); it routes like an entity id.
        launch.putExtra(KEY_ENTITY_ID, data[KEY_ENTITY_ID] ?: data[KEY_CONVERSATION_ID])
        launch.putExtra(KEY_DEEP_LINK, data[KEY_DEEP_LINK])
        launch.putExtra("offer_id", data["offer_id"])
        return PendingIntent.getActivity(
            context,
            notificationId(data),
            launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    private fun offerIntent(data: Map<String, String>, decision: String): PendingIntent {
        val id = data["offer_id"] ?: data[KEY_ENTITY_ID].orEmpty()
        // Explicit, immutable Activity intent: no broadcast trampoline and no exported action endpoint.
        val intent = Intent().setClassName(context.packageName, PartnerOfferCategory.ACTIVITY)
            .setData(Uri.Builder().scheme("partner-offer").authority(decision)
                .appendPath(data[KEY_TYPE]).appendPath(id).build())
            .putExtra("decision", decision)
        PartnerOfferCategory.EXTRA_KEYS.forEach { intent.putExtra(it, data[it]) }
        return PendingIntent.getActivity(context, 0, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
    }

    /**
     * POST_NOTIFICATIONS is a runtime permission from Android 13. Posting
     * without it throws a SecurityException, so a silent no-op here is the
     * difference between a declined permission and a crash on delivery.
     */
    private fun canPost(): Boolean = Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(
        context,
        Manifest.permission.POST_NOTIFICATIONS,
    ) == PackageManager.PERMISSION_GRANTED

    /**
     * A stable id per logical subject, so an updated notification REPLACES the
     * previous one instead of stacking. The subject is the entity id
     * (conversation, post, …) when the server sent one; falls back to a
     * distinct id, because collapsing unrelated notifications onto one id
     * silently loses all but the last.
     */
    private fun notificationId(data: Map<String, String>): Int =
        (data["offer_id"] ?: data[KEY_ENTITY_ID] ?: data[KEY_CONVERSATION_ID] ?: data[KEY_SUBJECT])?.hashCode()
            ?: data.hashCode()

    private fun NotificationChannelSpec.compatPriority(): Int = when (importance) {
        NotificationManager.IMPORTANCE_HIGH -> NotificationCompat.PRIORITY_HIGH
        NotificationManager.IMPORTANCE_LOW -> NotificationCompat.PRIORITY_LOW
        else -> NotificationCompat.PRIORITY_DEFAULT
    }

    companion object {
        const val KEY_TITLE = "title"
        const val KEY_BODY = "body"
        const val KEY_TYPE = "type"
        const val KEY_SUBJECT = "subject_id"
        const val KEY_ENTITY_ID = "entity_id"
        const val KEY_DEEP_LINK = "deep_link"

        /** Chat parity (CONTRACT §8): the conversation's tone and id on a chat push. */
        const val KEY_ALERT_TONE = "alert_tone"
        const val KEY_CONVERSATION_ID = "conversation_id"

    }
}

/** Delivery/service Partner is not a restaurant, seller or customer notification destination. */
internal fun acceptsInstalledAppNotification(app: PushApp, type: String?): Boolean =
    if (app == PushApp.FEAST_KITCHEN) type == "food_order_new" || type?.startsWith("food_kitchen_") == true || type?.startsWith("food.restaurant.") == true
    else app != PushApp.PARTNER || type?.let {
        it.startsWith("doorstep.pro.") || it.startsWith("food_delivery_") || it.startsWith("captain.") || it.startsWith("commerce.delivery.")
    } == true
