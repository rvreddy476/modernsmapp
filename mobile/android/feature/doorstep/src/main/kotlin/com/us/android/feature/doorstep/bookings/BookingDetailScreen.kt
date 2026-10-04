package com.us.android.feature.doorstep.bookings

import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.CancelPreviewDto
import com.us.android.feature.doorstep.data.ExtraDto
import com.us.android.feature.doorstep.domain.BillRules
import com.us.android.feature.doorstep.domain.BookingRules
import com.us.android.feature.doorstep.domain.BookingStatus
import com.us.android.feature.doorstep.domain.StepState
import com.us.android.feature.doorstep.domain.TimelineStep
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.payment.BillPayState
import com.us.android.feature.doorstep.payment.DoorstepPaymentRequest
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.MoneyRow
import com.us.android.feature.doorstep.ui.Pill
import com.us.android.feature.doorstep.ui.SectionLabel
import com.us.android.feature.doorstep.ui.StarPicker
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.listPadding
import com.us.android.feature.doorstep.ui.slotRangeText
import com.us.android.feature.doorstep.ui.tone
import com.us.android.feature.doorstep.ui.toneColor

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun BookingDetailScreen(
    onBack: () -> Unit,
    onReschedule: (bookingId: String) -> Unit,
    onOpenPayment: (DoorstepPaymentRequest) -> Unit,
    onAbandonPayment: (DoorstepPaymentRequest) -> Unit,
    viewModel: BookingDetailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val pay by viewModel.billPayment.state.collectAsStateWithLifecycle()
    val context = LocalContext.current

    // Coming back from the background: the stream does not gap-fill, so re-read.
    LifecycleResumeEffect(viewModel) {
        viewModel.refreshNow()
        onPauseOrDispose { }
    }
    val opening = pay as? BillPayState.Opening
    LaunchedEffect(opening?.request?.attempt) { opening?.let { onOpenPayment(it.request) } }
    DisposableEffect(opening?.request) {
        val request = opening?.request
        onDispose { if (request != null) onAbandonPayment(request) }
    }
    LaunchedEffect(state.shareUrl) {
        val url = state.shareUrl ?: return@LaunchedEffect
        val send = Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_TEXT, "Follow my Doorstep visit: $url")
        }
        context.startActivity(Intent.createChooser(send, "Share visit status"))
        viewModel.consumeShareUrl()
    }

    var askSos by rememberSaveable { mutableStateOf(false) }
    state.cancelPreview?.let { preview ->
        CancelDialog(preview = preview, busy = state.cancelling, onConfirm = viewModel::confirmCancel, onDismiss = viewModel::dismissCancel)
    }
    if (askSos) {
        SosDialog(
            onSend = { note ->
                askSos = false
                viewModel.sos(note)
            },
            onDismiss = { askSos = false },
        )
    }

    DoorstepScreen(
        title = state.booking?.serviceName ?: "Booking",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        val booking = state.booking
        when {
            state.loading && booking == null -> LoadingPane()
            booking == null -> MessagePane(
                title = "Couldn't load this booking",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::refreshNow,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                item { StatusHeader(booking, state.status, state.live) }

                state.timeline?.let { steps -> item { Timeline(steps) } } ?: item { OffRamp(booking, state.status) }

                state.startOtp?.let { otp -> item { OtpCard(otp) } }

                booking.professional?.let { pro ->
                    item {
                        DoorstepCard {
                            Text("Your professional", style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textDim)
                            Text(pro.firstName, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
                            val rating = pro.ratingAvg?.let { String.format(java.util.Locale.ENGLISH, "★ %.1f", it) } ?: "New"
                            Text(
                                "$rating · ${pro.jobsCompleted} jobs done",
                                style = MaterialTheme.typography.bodySmall,
                                color = UsTheme.extended.textMuted,
                            )
                            if (booking.requireFemalePro) InfoNote("You asked for a woman professional.", tone = Tone.Accent)
                        }
                    }
                }

                item {
                    DoorstepCard {
                        Text("Visit address", style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textDim)
                        Text(
                            listOfNotNull(booking.address.line1, booking.address.line2, booking.address.landmark, booking.address.locality, booking.address.pincode)
                                .filter { it.isNotBlank() }
                                .joinToString(", "),
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textSecondary,
                        )
                    }
                }

                if (BookingRules.extrasVisible(state.status) && (state.extras.isNotEmpty() || state.bill != null)) {
                    item { SectionLabel("Extras") }
                    items(state.extrasSummary.proposed, key = { it.id }) { extra ->
                        ProposedExtra(
                            extra = extra,
                            busy = state.decidingExtraId == extra.id,
                            onApprove = { viewModel.approveExtra(extra.id) },
                            onDecline = { viewModel.declineExtra(extra.id) },
                        )
                    }
                    item {
                        DoorstepCard {
                            state.extras.filter { it.status != "proposed" }.forEach { extra ->
                                MoneyRow("${extra.name} × ${extra.quantity} (${extra.status})", Paise(extra.totalPaise), muted = extra.status !in AGREED)
                            }
                            MoneyRow("Extras you approved", state.extrasSummary.agreedTotal, emphasise = true)
                            state.bill?.let { bill ->
                                MoneyRow("Extras bill (${bill.status.replace('_', ' ')})", Paise(bill.amountPaise))
                                if (BillRules.payable(bill)) {
                                    UsButton(
                                        text = "Pay ${Paise(bill.amountPaise).toRupeeText()}",
                                        onClick = viewModel::payBill,
                                        loading = pay is BillPayState.Starting || pay is BillPayState.Confirming,
                                        modifier = Modifier.fillMaxWidth(),
                                    )
                                }
                            }
                            BillPayNote(pay)
                        }
                    }
                }

                item { SectionLabel("Payment") }
                item {
                    DoorstepCard {
                        booking.items.forEach { line -> MoneyRow(line.name, Paise(line.lineTotalPaise)) }
                        MoneyRow("Total (GST included)", Paise(booking.totalPaise), emphasise = true)
                        MoneyRow("Paid", Paise(booking.paidPaise), muted = true)
                        if (booking.refundedPaise > 0) MoneyRow("Refunded", Paise(booking.refundedPaise), muted = true)
                        if (booking.cancellationFeePaise > 0) MoneyRow("Cancellation fee", Paise(booking.cancellationFeePaise), muted = true)
                        if (booking.outstandingPaise > 0) MoneyRow("Outstanding", Paise(booking.outstandingPaise))
                        if (state.status == BookingStatus.PENDING_PAYMENT) {
                            InfoNote("Waiting for your payment to be confirmed. If money left your account, this confirms on its own.", tone = Tone.Warning)
                        }
                    }
                }

                if (BookingRules.afterCareAvailable(booking)) {
                    item { SectionLabel("How did it go?") }
                    item { RatingCard(rated = state.rated, busy = state.submitting, onRate = viewModel::rate) }
                    item {
                        ReworkCard(
                            requested = state.reworkRequested || state.rework.isNotEmpty(),
                            statuses = state.rework.map { it.status },
                            busy = state.submitting,
                            onRequest = viewModel::requestRework,
                        )
                    }
                }

                if (BookingRules.safetyAvailable(state.status)) {
                    item { SectionLabel("Safety") }
                    item {
                        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                            UsPillButton(text = "SOS", onClick = { askSos = true })
                            UsPillButton(text = "Share status", onClick = viewModel::share, filled = false)
                        }
                    }
                }

                if (booking.canReschedule || booking.canCancel) {
                    item { SectionLabel("Manage") }
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                            if (booking.canReschedule) {
                                UsSecondaryButton(text = "Reschedule", onClick = { onReschedule(booking.id) }, modifier = Modifier.fillMaxWidth())
                            }
                            if (booking.canCancel) {
                                UsSecondaryButton(
                                    text = if (state.cancelling) "Checking the fee…" else "Cancel booking",
                                    onClick = viewModel::previewCancel,
                                    enabled = !state.cancelling,
                                    modifier = Modifier.fillMaxWidth(),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

private val AGREED = setOf("approved", "billed")

@Composable
private fun StatusHeader(booking: BookingDto, status: BookingStatus, live: Boolean) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Pill(status.label, status.tone())
            Spacer(Modifier.width(UsTheme.spacing.m))
            if (BookingRules.isLive(status)) {
                Text(
                    if (live) "Live" else "Updating every few seconds",
                    style = MaterialTheme.typography.labelSmall,
                    color = if (live) UsTheme.extended.statusSuccess else UsTheme.extended.textDim,
                )
            }
        }
        Text(slotRangeText(booking.slotStart, booking.slotEnd), style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        if (booking.parentBookingId != null) {
            Text("Rework visit — free", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
    }
}

@Composable
private fun Timeline(steps: List<TimelineStep>) {
    DoorstepCard {
        steps.forEach { step ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                val color = when (step.state) {
                    StepState.DONE -> UsTheme.extended.statusSuccess
                    StepState.CURRENT -> UsTheme.extended.accentSolid
                    StepState.UPCOMING -> UsTheme.extended.textGhost
                }
                Box(Modifier.size(10.dp).background(color, CircleShape))
                Spacer(Modifier.width(UsTheme.spacing.l))
                Text(
                    step.label,
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = if (step.state == StepState.CURRENT) FontWeight.SemiBold else FontWeight.Normal,
                    color = if (step.state == StepState.UPCOMING) UsTheme.extended.textDim else UsTheme.extended.textPrimary,
                )
            }
        }
    }
}

@Composable
private fun OffRamp(booking: BookingDto, status: BookingStatus) {
    val text = when (status) {
        BookingStatus.CANCELLED -> "This booking was cancelled." +
            if (booking.refundedPaise > 0) " ${Paise(booking.refundedPaise).toRupeeText()} is refunded to you." else ""
        BookingStatus.EXPIRED -> "The hold ended before payment. If money left your account, it is refunded in full."
        BookingStatus.PRO_NO_SHOW -> "We couldn't get a professional to you. You get a full refund."
        BookingStatus.CUSTOMER_NO_SHOW -> "The professional couldn't reach you at the address."
        else -> "We're updating this booking."
    }
    DoorstepCard { InfoNote(text, tone = status.tone()) }
}

/**
 * The start code. Large and centred: the professional reads it off this
 * screen at the door. Shown only while [BookingRules.visibleStartOtp] allows.
 */
@Composable
private fun OtpCard(otp: String) {
    DoorstepCard(highlighted = true) {
        Text("Start code", style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textDim)
        Text(
            otp,
            style = MaterialTheme.typography.displaySmall,
            fontWeight = FontWeight.Bold,
            letterSpacing = 8.sp,
            textAlign = TextAlign.Center,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.fillMaxWidth(),
        )
        Text(
            "Share it only when the professional is at your door and about to start.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun ProposedExtra(extra: ExtraDto, busy: Boolean, onApprove: () -> Unit, onDecline: () -> Unit) {
    DoorstepCard(highlighted = true) {
        Text("The professional suggests", style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textDim)
        MoneyRow(if (extra.quantity > 1) "${extra.name} × ${extra.quantity}" else extra.name, Paise(extra.totalPaise), emphasise = true)
        Text(
            "Fixed rate-card price, GST included. Nothing is added unless you approve.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsPillButton(text = "Approve", onClick = onApprove, busy = busy)
            UsPillButton(text = "Decline", onClick = onDecline, filled = false, enabled = !busy)
        }
    }
}

@Composable
private fun BillPayNote(pay: BillPayState) {
    when (pay) {
        is BillPayState.Confirming -> InfoNote("Confirming your payment with the bank…", tone = Tone.Accent)
        is BillPayState.Paid -> InfoNote("Extras paid. Thank you.", tone = Tone.Positive)
        is BillPayState.Failed -> InfoNote(pay.reason ?: "The payment didn't go through. Try again.", tone = Tone.Danger)
        is BillPayState.StillConfirming -> InfoNote("Still confirming. If money left your account, this clears on its own.", tone = Tone.Warning)
        is BillPayState.Refunding -> InfoNote("That payment is being refunded.", tone = Tone.Warning)
        else -> Unit
    }
}

@Composable
private fun RatingCard(rated: Boolean, busy: Boolean, onRate: (Int, String) -> Unit) {
    var stars by rememberSaveable { mutableIntStateOf(0) }
    var comment by rememberSaveable { mutableStateOf("") }
    DoorstepCard {
        if (rated) {
            InfoNote("Thanks — your rating helps other customers.", tone = Tone.Positive)
            return@DoorstepCard
        }
        Text("Rate your professional", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        StarPicker(stars = stars, onPick = { stars = it })
        UsTextField(comment, { comment = it.take(MAX_COMMENT) }, "Anything to add? (optional)", Modifier.fillMaxWidth(), singleLine = false)
        UsPillButton(text = "Submit rating", onClick = { onRate(stars, comment) }, enabled = stars > 0, busy = busy)
    }
}

@Composable
private fun ReworkCard(requested: Boolean, statuses: List<String>, busy: Boolean, onRequest: (String) -> Unit) {
    var reason by rememberSaveable { mutableStateOf("") }
    DoorstepCard {
        Text("Not happy with the work?", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        if (requested) {
            val status = statuses.lastOrNull()?.replace('_', ' ') ?: "requested"
            InfoNote("Rework $status. We'll schedule a free visit.", tone = Tone.Accent)
            return@DoorstepCard
        }
        Text(
            "Ask for a free redo within the rework window.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        UsTextField(reason, { reason = it.take(MAX_COMMENT) }, "What needs redoing?", Modifier.fillMaxWidth(), singleLine = false)
        UsPillButton(text = "Request rework", onClick = { onRequest(reason) }, enabled = reason.isNotBlank(), busy = busy, filled = false)
    }
}

@Composable
private fun CancelDialog(preview: CancelPreviewDto, busy: Boolean, onConfirm: (String) -> Unit, onDismiss: () -> Unit) {
    var reason by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (preview.allowed) "Cancel this booking?" else "Can't cancel now") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                if (preview.allowed) {
                    MoneyRow("Cancellation fee", Paise(preview.feePaise))
                    MoneyRow("Refund to you", Paise(preview.refundPaise), emphasise = true)
                    UsTextField(reason, { reason = it.take(MAX_COMMENT) }, "Reason (optional)", Modifier.fillMaxWidth())
                } else {
                    Text("The job has started. If something is wrong, use SOS or contact support.")
                }
            }
        },
        confirmButton = {
            if (preview.allowed) {
                TextButton(onClick = { onConfirm(reason) }, enabled = !busy) {
                    Text("Cancel booking", color = toneColor(Tone.Danger))
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Keep booking", color = UsTheme.extended.textMuted) }
        },
        containerColor = UsTheme.extended.bgRaised,
        titleContentColor = UsTheme.extended.textPrimary,
        textContentColor = UsTheme.extended.textSecondary,
    )
}

@Composable
private fun SosDialog(onSend: (String) -> Unit, onDismiss: () -> Unit) {
    var note by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(UsIcons.ShieldAlert, contentDescription = null, tint = UsTheme.extended.statusDanger) },
        title = { Text("Alert our safety team?") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text("We'll call you right away. If you're in immediate danger, call 112 first.")
                UsTextField(note, { note = it.take(MAX_COMMENT) }, "What's happening? (optional)", Modifier.fillMaxWidth(), singleLine = false)
            }
        },
        confirmButton = {
            TextButton(onClick = { onSend(note) }) { Text("Send SOS", color = UsTheme.extended.statusDanger) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Not now", color = UsTheme.extended.textMuted) }
        },
        containerColor = UsTheme.extended.bgRaised,
        titleContentColor = UsTheme.extended.textPrimary,
        textContentColor = UsTheme.extended.textSecondary,
    )
}

private const val MAX_COMMENT = 1000
