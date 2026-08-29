package com.us.android.core.call.platform

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.media.AudioManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.getSystemService
import com.us.android.core.call.CallAudioController
import com.us.android.core.call.CallNotifier
import com.us.android.core.call.service.OngoingCallService
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.core.notifications.NotificationPresenter
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The ringing notification. Full-screen intent on the CALLS channel, carrying
 * the SAME launch-intent extras contract as every push
 * ([NotificationPresenter.KEY_TYPE]/[NotificationPresenter.KEY_ENTITY_ID]),
 * so one navigation path serves socket rings, push rings and taps alike.
 *
 * Privacy: the notification carries NO caller name or identity — "Incoming
 * call" only. The call surface resolves who is calling AFTER the device is
 * unlocked, so a locked screen enumerates nothing.
 */
@Singleton
class AndroidCallNotifier @Inject constructor(
    @ApplicationContext private val context: Context,
) : CallNotifier {

    override fun showIncoming(callId: String, callerId: String, video: Boolean) {
        val launch = context.packageManager.getLaunchIntentForPackage(context.packageName) ?: return
        launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        launch.putExtra(NotificationPresenter.KEY_TYPE, if (video) "incoming_video_call" else "incoming_call")
        launch.putExtra(NotificationPresenter.KEY_ENTITY_ID, callId)
        val fullScreen = PendingIntent.getActivity(
            context,
            callId.hashCode(),
            launch,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(context, NotificationChannelSpec.CALLS.id)
            .setContentTitle(if (video) "Incoming video call" else "Incoming call")
            .setContentText("Tap to answer or decline")
            .setSmallIcon(android.R.drawable.sym_call_incoming)
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setOngoing(true)
            .setContentIntent(fullScreen)
            .setFullScreenIntent(fullScreen, true)
            .build()
        runCatching {
            NotificationManagerCompat.from(context).notify(INCOMING_NOTIFICATION_ID, notification)
        }
    }

    override fun clearIncoming() {
        NotificationManagerCompat.from(context).cancel(INCOMING_NOTIFICATION_ID)
    }

    override fun startOngoing(peerId: String, video: Boolean) {
        val intent = Intent(context, OngoingCallService::class.java)
            .putExtra(OngoingCallService.EXTRA_VIDEO, video)
        runCatching { context.startForegroundService(intent) }
    }

    override fun stopOngoing() {
        runCatching { context.stopService(Intent(context, OngoingCallService::class.java)) }
    }

    private companion object {
        const val INCOMING_NOTIFICATION_ID = 0x0CA11
    }
}

/**
 * Communication-mode audio for the call's lifetime. Speaker defaults follow
 * the call kind: video → speaker, audio → earpiece.
 */
@Singleton
class AndroidCallAudioController @Inject constructor(
    @ApplicationContext private val context: Context,
) : CallAudioController {

    private val audioManager: AudioManager? get() = context.getSystemService()

    override fun onCallStarted(video: Boolean) {
        audioManager?.apply {
            mode = AudioManager.MODE_IN_COMMUNICATION
            isSpeakerphoneOn = video
        }
    }

    override fun setSpeaker(on: Boolean) {
        audioManager?.isSpeakerphoneOn = on
    }

    override fun onCallEnded() {
        audioManager?.apply {
            isSpeakerphoneOn = false
            mode = AudioManager.MODE_NORMAL
        }
    }
}
