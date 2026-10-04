package com.us.android.feature.doorstep.checkout

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.domain.BookingRules
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.payment.DoorstepPaymentRequest
import com.us.android.feature.doorstep.ui.BottomAction
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.MoneyRow
import com.us.android.feature.doorstep.ui.SectionLabel
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.durationText
import com.us.android.feature.doorstep.ui.visitText
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun CheckoutScreen(
    onBack: () -> Unit,
    onOpenPayment: (DoorstepPaymentRequest) -> Unit,
    onAbandonPayment: (DoorstepPaymentRequest) -> Unit,
    onPickAnotherSlot: () -> Unit,
    onOpenBooking: (bookingId: String) -> Unit,
    onOpenBookings: () -> Unit,
    onOpenOutstanding: () -> Unit,
    viewModel: CheckoutViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // The Activity opens the sheet; this screen only asks for it, once per attempt.
    val opening = state as? CheckoutState.OpeningPayment
    LaunchedEffect(opening?.request?.attempt) { opening?.let { onOpenPayment(it.request) } }
    DisposableEffect(opening?.request) {
        val request = opening?.request
        onDispose { if (request != null) onAbandonPayment(request) }
    }
    LaunchedEffect(state) {
        (state as? CheckoutState.Paid)?.let { onOpenBooking(it.bookingId) }
    }

    DoorstepScreen(
        title = "Checkout",
        onBack = onBack,
        bottomBar = {
            (state as? CheckoutState.Ready)?.let { ready ->
                BottomAction(
                    label = "Pay ${Paise(ready.quote.totalPaise).toRupeeText()}",
                    onClick = viewModel::book,
                    loading = ready.placing,
                    enabled = !ready.placing,
                    summary = "UPI or card. No cash. A professional is held for 10 minutes while you pay.",
                )
            }
        },
    ) { padding ->
        when (val s = state) {
            CheckoutState.Loading -> LoadingPane()
            is CheckoutState.Ready -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 24.dp),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                DoorstepCard {
                    Text(visitText(s.slotStart, s.quote.durationMinutes), style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                    Text(
                        "${durationText(s.quote.durationMinutes)} · ${s.address.label}: ${s.address.line1}, ${s.address.locality}",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                    if (s.requireFemalePro) InfoNote("A woman professional will be assigned.", tone = Tone.Accent)
                }
                SectionLabel("Bill")
                DoorstepCard {
                    s.quote.lines.forEach { line ->
                        MoneyRow(
                            label = if (line.quantity > 1) "${line.name} × ${line.quantity}" else line.name,
                            amount = Paise(line.lineTotalPaise),
                        )
                    }
                    MoneyRow("Of which taxable value", Paise(s.quote.taxablePaise), muted = true)
                    MoneyRow("Of which GST", Paise(s.quote.taxPaise), muted = true)
                    MoneyRow("Total", Paise(s.quote.totalPaise), emphasise = true)
                    InfoNote(s.quote.taxNote)
                }
                UsTextField(
                    value = s.notes,
                    onValueChange = viewModel::onNotes,
                    label = "Notes for the professional (optional)",
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = false,
                )
                s.refusal?.let { refusal ->
                    InfoNote(refusal, tone = Tone.Danger)
                    if (s.pickAnotherSlot) {
                        UsSecondaryButton(text = "Pick another slot", onClick = onPickAnotherSlot, modifier = Modifier.fillMaxWidth())
                    }
                }
            }
            is CheckoutState.OpeningPayment -> Column {
                LoadingPane(label = "Opening payment…")
                HoldCountdown(s.holdExpiresAt)
            }
            is CheckoutState.Confirming -> LoadingPane(label = "Confirming your payment with the bank…")
            is CheckoutState.StillConfirming -> MessagePane(
                title = "Still confirming",
                body = "Your bank hasn't told us yet. If money left your account, the booking confirms on its own — " +
                    "you'll see it in My bookings. You won't be charged twice.",
                icon = UsIcons.Clock,
                primaryLabel = "Go to my bookings",
                onPrimary = onOpenBookings,
                secondaryLabel = "Check again",
                onSecondary = viewModel::retryPayment,
            )
            is CheckoutState.Paid -> LoadingPane(label = "Booked!")
            is CheckoutState.PaymentFailed -> Column(Modifier.padding(padding)) {
                MessagePane(
                    title = "Payment didn't go through",
                    body = s.reason ?: "Your slot is still held for a few minutes. Try paying again.",
                    icon = UsIcons.CreditCard,
                    iconTint = UsTheme.extended.statusDanger,
                    primaryLabel = "Pay again",
                    onPrimary = viewModel::retryPayment,
                    secondaryLabel = "Pick another slot",
                    onSecondary = onPickAnotherSlot,
                    modifier = Modifier.weight(1f),
                )
                HoldCountdown(s.holdExpiresAt)
            }
            is CheckoutState.Refunding -> MessagePane(
                title = "Refund on its way",
                body = "The payment landed after your hold ended, so the booking couldn't be confirmed. " +
                    "The full amount is being returned to you.",
                icon = UsIcons.RotateCcw,
                primaryLabel = "Book again",
                onPrimary = onPickAnotherSlot,
                secondaryLabel = "My bookings",
                onSecondary = onOpenBookings,
            )
            CheckoutState.HoldExpired -> MessagePane(
                title = "Your hold ended",
                body = "We hold a professional for 10 minutes. Pick a slot again to book.",
                icon = UsIcons.Clock,
                primaryLabel = "Pick a slot",
                onPrimary = onPickAnotherSlot,
            )
            CheckoutState.BlockedByDues -> MessagePane(
                title = "Pending dues",
                body = "Pay your unpaid extras from an earlier visit to book again.",
                icon = UsIcons.CreditCard,
                primaryLabel = "Pay dues",
                onPrimary = onOpenOutstanding,
            )
            is CheckoutState.Failed -> MessagePane(
                title = "Couldn't load checkout",
                body = s.message,
                primaryLabel = "Try again",
                onPrimary = viewModel::reload,
                secondaryLabel = "Pick another slot",
                onSecondary = onPickAnotherSlot,
            )
        }
    }
}

/** "Professional held for 8:41". Ticks once a second from the server's hold_expires_at. */
@Composable
private fun HoldCountdown(holdExpiresAt: Instant?) {
    if (holdExpiresAt == null) return
    val remaining by produceState(BookingRules.holdRemaining(holdExpiresAt, Instant.now()), holdExpiresAt) {
        while (true) {
            value = BookingRules.holdRemaining(holdExpiresAt, Instant.now())
            if (value?.isZero == true) break
            delay(TICK_MILLIS)
        }
    }
    val left = remaining ?: return
    InfoNote(
        text = if (left.isZero) "Your hold has ended." else "Professional held for ${BookingRules.countdownText(left)}",
        tone = if (left.seconds < WARN_SECONDS) Tone.Warning else Tone.Accent,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
    )
}

private const val TICK_MILLIS = 1_000L
private const val WARN_SECONDS = 120
