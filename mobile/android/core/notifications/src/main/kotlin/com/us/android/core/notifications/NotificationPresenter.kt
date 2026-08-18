package com.us.android.core.notifications

import android.Manifest
import android.app.NotificationManager
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
 */
@Singleton
class NotificationPresenter @Inject constructor(
    @ApplicationContext private val context: Context,
) {

    fun present(data: Map<String, String>) {
        val title = data[KEY_TITLE] ?: return
        val body = data[KEY_BODY].orEmpty()
        val channel = NotificationChannelSpec.forType(data[KEY_TYPE])

        if (!canPost()) return

        val notification = NotificationCompat.Builder(context, channel.id)
            .setContentTitle(title)
            .setContentText(body)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setAutoCancel(true)
            .setPriority(channel.compatPriority())
            .build()

        NotificationManagerCompat.from(context).notify(notificationId(data), notification)
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
     * previous one instead of stacking. Falls back to a distinct id when the
     * server sent no subject, because collapsing unrelated notifications onto
     * one id silently loses all but the last.
     */
    private fun notificationId(data: Map<String, String>): Int =
        data[KEY_SUBJECT]?.hashCode() ?: data.hashCode()

    private fun NotificationChannelSpec.compatPriority(): Int = when (importance) {
        NotificationManager.IMPORTANCE_HIGH -> NotificationCompat.PRIORITY_HIGH
        NotificationManager.IMPORTANCE_LOW -> NotificationCompat.PRIORITY_LOW
        else -> NotificationCompat.PRIORITY_DEFAULT
    }

    private companion object {
        const val KEY_TITLE = "title"
        const val KEY_BODY = "body"
        const val KEY_TYPE = "type"
        const val KEY_SUBJECT = "subject_id"
    }
}
