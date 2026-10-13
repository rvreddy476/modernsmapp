package com.us.android.feature.doorsteppro.offers

import android.app.NotificationManager
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.RingtoneManager
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.getSystemService
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.doorsteppro.domain.OfferCountdown
import com.us.android.feature.doorsteppro.domain.OfferWindow
import com.us.android.feature.doorsteppro.model.Paise
import com.us.android.feature.doorsteppro.model.toRupeeText
import com.us.android.feature.doorsteppro.ui.slotRangeText
import kotlinx.coroutines.delay

/** App-only incoming request card. No web popup, call-style full-screen intent or alarm bypass. */
@Composable
fun IncomingRequestsHost(userId: String, vm: IncomingRequestsViewModel, onAccepted: (String) -> Unit, showPopup: Boolean = true) {
    val state by vm.state.collectAsStateWithLifecycle()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val lifecycleState by lifecycle.currentStateFlow.collectAsStateWithLifecycle()
    LaunchedEffect(vm, lifecycle, userId) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { vm.listen(userId) }
    }
    DisposableEffect(vm) { onDispose { vm.reset() } }
    LaunchedEffect(state.acceptedBookingId) {
        state.acceptedBookingId?.let { onAccepted(it); vm.acceptedHandled() }
    }
    if (!showPopup) return
    val offer = state.incoming ?: return
    val window = OfferCountdown.of(offer.expiresAt).at(state.now) as? OfferWindow.Open ?: return
    RequestRing(
        offer.id,
        remainingMillis = window.remainingSeconds * 1_000L,
        enabled = !state.responding && lifecycleState.isAtLeast(Lifecycle.State.RESUMED),
        alerts = vm.offerAlerts,
    )
    AlertDialog(
        onDismissRequest = { if (!state.responding) vm.dismiss() },
        title = { Text("New service request") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(offer.serviceName, style = MaterialTheme.typography.titleMedium)
                Text(offer.locality)
                Text(slotRangeText(offer.slotStart, offer.slotEnd))
                Text("Estimated earnings: ${Paise(offer.earningEstimatePaise).toRupeeText()}")
                Text("${window.remainingSeconds}s to respond", color = MaterialTheme.colorScheme.primary)
                if (state.offers.size > 1) Text("${state.offers.size} requests waiting")
                state.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                Text("Full address and customer details are available after acceptance.", style = MaterialTheme.typography.bodySmall)
            }
        },
        confirmButton = {
            TextButton(onClick = { vm.respond(true) }, enabled = !state.responding) {
                Text(if (state.responding) "Please wait…" else "Accept")
            }
        },
        dismissButton = { TextButton(onClick = { vm.respond(false) }, enabled = !state.responding) { Text("Decline") } },
    )
}

/** Repeats a notification tone while visible. Respect silence, DND and the user's channel settings. */
@Composable
private fun RequestRing(offerId: String, remainingMillis: Long, enabled: Boolean, alerts: DoorstepOfferAlertCoordinator) {
    val context = LocalContext.current
    DisposableEffect(offerId, enabled) {
        onDispose {
            if (enabled) context.getSystemService<NotificationManager>()?.cancel(offerId.hashCode())
        }
    }
    LaunchedEffect(offerId, enabled) {
        if (!enabled) return@LaunchedEffect
        val manager = context.getSystemService<NotificationManager>() ?: return@LaunchedEffect
        val audio = context.getSystemService<AudioManager>() ?: return@LaunchedEffect
        // A push may have sounded before the API populated this card. Keep the
        // card and countdown, but do not start a second competing ringtone.
        if (!alerts.claimSound(offerId, remainingMillis)) return@LaunchedEffect
        var tone: android.media.Ringtone? = null
        val started = android.os.SystemClock.elapsedRealtime()
        try {
            while (android.os.SystemClock.elapsedRealtime() - started < 30_000L) {
                val channel = manager.getNotificationChannel(NotificationChannelSpec.DOORSTEP_PRO_OFFERS.id)
                val mayRing = manager.areNotificationsEnabled() &&
                    audio.ringerMode == AudioManager.RINGER_MODE_NORMAL &&
                    manager.currentInterruptionFilter == NotificationManager.INTERRUPTION_FILTER_ALL &&
                    (channel?.importance ?: 0) >= NotificationManager.IMPORTANCE_DEFAULT
                if (mayRing && channel?.sound != null) {
                    if (tone == null) tone = RingtoneManager.getRingtone(context, channel.sound)?.apply {
                        audioAttributes = AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_NOTIFICATION_EVENT).build()
                    }
                    if (tone?.isPlaying == false) tone?.play()
                } else tone?.stop()
                delay(2_000L)
            }
        } catch (_: SecurityException) {
            // A sound/notification permission changed while the card was open.
        } finally {
            tone?.stop()
        }
    }
}
