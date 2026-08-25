package com.us.android.core.notifications

import android.Manifest
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

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
) {

    fun present(data: Map<String, String>) {
        val title = data[KEY_TITLE] ?: return
        val body = data[KEY_BODY].orEmpty()
        val type = data[KEY_TYPE]
        val channel = NotificationChannelSpec.forType(type)

        // Push/socket de-duplication, client half: while the app is visible
        // the session socket already delivered this chat message to the open
        // surface. (Background chat pushes carry a `notification` block and
        // are system-rendered without reaching this code at all.)
        if (type in CHAT_TYPES && foreground.isForeground) return

        if (!canPost()) return

        val notification = NotificationCompat.Builder(context, channel.id)
            .setContentTitle(title)
            .setContentText(body)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setAutoCancel(true)
            .setPriority(channel.compatPriority())
            .apply { tapIntent(data)?.let(::setContentIntent) }
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
        launch.putExtra(KEY_ENTITY_ID, data[KEY_ENTITY_ID])
        launch.putExtra(KEY_DEEP_LINK, data[KEY_DEEP_LINK])
        return PendingIntent.getActivity(
            context,
            notificationId(data),
            launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    /**
     * POST_NOTIFICATIONS is a runtime permission from Android 13. Posting
     * without it throws a SecurityException, so a silent no-op here is the
     * difference between a declined permission and a crash on delivery.
     */
    private fun canPost(): Boolean = ContextCompat.checkSelfPermission(
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
        (data[KEY_ENTITY_ID] ?: data[KEY_SUBJECT])?.hashCode() ?: data.hashCode()

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

        /** The server's chat notification types. */
        val CHAT_TYPES = setOf("dm", "message_request")
    }
}
