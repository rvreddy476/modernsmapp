package com.us.android.feature.post.createhub

import android.app.PendingIntent
import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationChannelCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.work.ForegroundInfo

/** Stable: renaming it orphans the user's choice for the channel. */
private const val UPLOAD_CHANNEL_ID = "video_uploads"
private const val UPLOAD_NOTIFICATION_ID = 0x5E1D

/**
 * The ongoing notification a big video upload runs under (2026-10-21).
 *
 * LOW importance: it is there so the system lets the upload run with the
 * screen off, not to interrupt anyone. The `dataSync` type is the one
 * Android names for uploads; the manifest declares it on WorkManager's
 * foreground service. Tapping it opens the app, where the pending tile
 * shows the real progress.
 */
internal fun uploadForegroundInfo(context: Context): ForegroundInfo {
    NotificationManagerCompat.from(context).createNotificationChannel(
        NotificationChannelCompat.Builder(UPLOAD_CHANNEL_ID, NotificationManagerCompat.IMPORTANCE_LOW)
            .setName("Video uploads")
            .setDescription("Shown while a long video uploads")
            .setShowBadge(false)
            .build(),
    )
    val open = context.packageManager.getLaunchIntentForPackage(context.packageName)?.let { intent ->
        PendingIntent.getActivity(context, 0, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }
    val notification = NotificationCompat.Builder(context, UPLOAD_CHANNEL_ID)
        .setSmallIcon(android.R.drawable.stat_sys_upload)
        .setContentTitle("Uploading your video")
        .setContentText("You can lock your phone. It keeps uploading.")
        .setCategory(NotificationCompat.CATEGORY_PROGRESS)
        .setProgress(0, 0, true)
        .setOngoing(true)
        .setOnlyAlertOnce(true)
        .setSilent(true)
        .setContentIntent(open)
        .build()
    return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
        ForegroundInfo(UPLOAD_NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
    } else {
        ForegroundInfo(UPLOAD_NOTIFICATION_ID, notification)
    }
}
