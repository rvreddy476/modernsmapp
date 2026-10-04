package com.us.android.feature.doorstep.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Modifier
import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.domain.BookingRules
import com.us.android.feature.doorstep.domain.ProChangeRules
import kotlinx.coroutines.delay
import java.time.Instant

/**
 * "Pick by 12:30 PM · 24:51 left" for a pro_unavailable booking, ticking once
 * a second from the server's `choice_deadline`. At zero it says what happens
 * next (a full refund) and calls [onLapsed] once so the screen re-reads the
 * booking; it never decides anything itself.
 */
@Composable
fun ChoiceDeadline(booking: BookingDto, modifier: Modifier = Modifier, onLapsed: () -> Unit = {}) {
    val deadline = booking.choiceDeadline ?: return
    val remaining by produceState(ProChangeRules.choiceRemaining(booking, Instant.now()), deadline) {
        while (true) {
            value = ProChangeRules.choiceRemaining(booking, Instant.now())
            if (value?.isZero == true) {
                onLapsed()
                break
            }
            delay(TICK_MILLIS)
        }
    }
    val left = remaining ?: return
    InfoNote(
        text = if (left.isZero) {
            "The time to choose is over. You get a full refund."
        } else {
            "Choose by ${slotTimeText(deadline)} · ${BookingRules.countdownText(left)} left, or we refund you in full."
        },
        tone = if (left.seconds < WARN_SECONDS) Tone.Danger else Tone.Warning,
        modifier = modifier,
    )
}

private const val TICK_MILLIS = 1_000L
private const val WARN_SECONDS = 300
