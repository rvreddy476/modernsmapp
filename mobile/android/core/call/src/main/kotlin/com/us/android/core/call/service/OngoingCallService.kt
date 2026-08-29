package com.us.android.core.call.service

import android.app.Notification
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import com.us.android.core.notifications.NotificationChannelSpec

/**
 * Keeps the process, microphone and (for video) camera alive while a call is
 * ACTIVE. Started when media connects, stopped on hangup/teardown. The
 * notification is deliberately anonymous — "Ongoing call", no peer identity —
 * for the same lock-screen privacy reason chat pushes are generic.
 */
class OngoingCallService : Service() {

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val video = intent?.getBooleanExtra(EXTRA_VIDEO, false) ?: false
        val notification: Notification =
            NotificationCompat.Builder(this, NotificationChannelSpec.CALLS.id)
                .setContentTitle("Ongoing call")
                .setContentText("Return to the app to manage the call")
                .setSmallIcon(android.R.drawable.sym_call_outgoing)
                .setCategory(NotificationCompat.CATEGORY_CALL)
                .setOngoing(true)
                .build()
        val type = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            var t = ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
            if (video) t = t or ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA
            t
        } else {
            0
        }
        if (type != 0) {
            startForeground(ONGOING_NOTIFICATION_ID, notification, type)
        } else {
            startForeground(ONGOING_NOTIFICATION_ID, notification)
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        stopForeground(STOP_FOREGROUND_REMOVE)
        super.onDestroy()
    }

    companion object {
        const val EXTRA_VIDEO = "video"
        private const val ONGOING_NOTIFICATION_ID = 0x0CA12
    }
}
