package com.us.feast.kitchen.alert

import android.Manifest
import android.annotation.SuppressLint
import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.MediaPlayer
import android.media.RingtoneManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ProcessLifecycleOwner
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.kitchen.queue.NewOrderAlert
import com.us.android.feature.kitchen.queue.NewOrderPush
import com.us.android.feature.kitchen.queue.PushRingHold
import com.us.feast.kitchen.KitchenActivity
import com.us.feast.kitchen.R
import dagger.hilt.android.qualifiers.ApplicationContext
import java.time.Duration
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The looping new-order alert.
 *
 * Foreground: the alarm tone loops in-app (MediaPlayer, alarm stream) until
 * every waiting order is accepted, rejected or expired. Background: an
 * INSISTENT notification on `kitchen_new_order` loops the same tone until it
 * is opened or the orders are answered. Never both at once.
 *
 * Called by the order queue on every tick; each call is cheap and idempotent.
 * A `food_order_new` push rings it too ([ringFromPush]), before the queue has
 * the order; [PushRingHold] keeps the stale queue from silencing that ring.
 * Main thread only (the queue's ViewModel scope; the push handler posts here).
 */
@Singleton
class KitchenNewOrderAlert @Inject constructor(
    @ApplicationContext private val context: Context,
) : NewOrderAlert {

    private var player: MediaPlayer? = null
    private var postedCount = NOT_POSTED
    private val hold = PushRingHold()

    override fun ring(waitingOrders: Int) {
        hold.queueRang()
        if (isForeground()) {
            cancelNotification()
            startLoop()
        } else {
            stopLoop()
            postNotification(waitingOrders)
        }
    }

    override fun silence() {
        if (!hold.queueMaySilence(Instant.now())) return
        stopLoop()
        cancelNotification()
    }

    override fun ringFromPush(ringFor: Duration, deepLink: String?) {
        hold.pushed(Instant.now())
        if (isForeground()) {
            cancelNotification()
            startLoop()
        } else {
            stopLoop()
            postNotification(waitingOrders = PUSHED, timeout = ringFor, deepLink = deepLink)
        }
    }

    private fun isForeground(): Boolean =
        ProcessLifecycleOwner.get().lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)

    @Suppress("TooGenericExceptionCaught")
    private fun startLoop() {
        if (player != null) return
        val tone = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_ALARM)
            ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
            ?: return
        player = try {
            MediaPlayer().apply {
                setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_ALARM)
                        .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                        .build(),
                )
                setDataSource(context, tone)
                isLooping = true
                prepare()
                start()
            }
        } catch (e: Exception) {
            // A device with no playable tone still shows the queue; never crash the kitchen over a sound.
            null
        }
    }

    private fun stopLoop() {
        player?.let {
            runCatching { it.stop() }
            it.release()
        }
        player = null
    }

    @SuppressLint("MissingPermission") // checked below
    private fun postNotification(waitingOrders: Int, timeout: Duration? = null, deepLink: String? = null) {
        if (waitingOrders == postedCount && timeout == null) return
        val manager = NotificationManagerCompat.from(context)
        if (!manager.areNotificationsEnabled()) return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            return
        }
        val open = PendingIntent.getActivity(
            context,
            // Its own request code: a later queue alarm must not inherit, or keep, the pushed order.
            if (deepLink == null) OPEN_KITCHEN else OPEN_PUSHED_ORDER,
            Intent(context, KitchenActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
                .apply {
                    // The pushed order, read by KitchenActivity exactly as a push card's extras were.
                    deepLink?.let {
                        putExtra(EXTRA_TYPE, NewOrderPush.TYPE)
                        putExtra(EXTRA_DEEP_LINK, it)
                    }
                },
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val notification = NotificationCompat.Builder(context, NotificationChannelSpec.KITCHEN_NEW_ORDER.id)
            .setSmallIcon(R.drawable.ic_stat_new_order)
            .setContentTitle(if (waitingOrders <= 1) "New order waiting" else "$waitingOrders new orders waiting")
            .setContentText("Accept before the timer runs out.")
            .setCategory(NotificationCompat.CATEGORY_ALARM)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setContentIntent(open)
            .setAutoCancel(true)
            .apply { timeout?.let { setTimeoutAfter(it.toMillis()) } }
            .build()
            .apply { flags = flags or Notification.FLAG_INSISTENT }
        manager.notify(NOTIFICATION_ID, notification)
        postedCount = waitingOrders
    }

    // Unconditional: a notification posted by an earlier process (killed while it looped) has no
    // count here, and must still stop once every order is answered (food audit L3).
    private fun cancelNotification() {
        NotificationManagerCompat.from(context).cancel(NOTIFICATION_ID)
        postedCount = NOT_POSTED
    }

    private companion object {
        const val NOTIFICATION_ID = 4101
        const val NOT_POSTED = -1

        // The keys KitchenActivity.readPush reads.
        const val EXTRA_TYPE = "type"
        const val EXTRA_DEEP_LINK = "deep_link"

        const val OPEN_KITCHEN = 0
        const val OPEN_PUSHED_ORDER = 1

        /** A push-rung alarm: the queue's first count always replaces it. */
        const val PUSHED = 0
    }
}
