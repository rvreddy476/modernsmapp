package com.us.android.feature.mopedu.rider.history

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.OutstandingCharge
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideStatus
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import com.us.android.feature.mopedu.rider.ui.CardHeading
import com.us.android.feature.mopedu.rider.ui.InfoNote
import com.us.android.feature.mopedu.rider.ui.LabeledValue
import com.us.android.feature.mopedu.rider.ui.LoadingPane
import com.us.android.feature.mopedu.rider.ui.MessageBanner
import com.us.android.feature.mopedu.rider.ui.MopeduCard
import com.us.android.feature.mopedu.rider.ui.MopeduPill
import com.us.android.feature.mopedu.rider.ui.PillTone
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** Past rides, with the "Outstanding" sheet for unpaid cancellation fees. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RideHistoryScreen(
    onBack: () -> Unit,
    onOpenPayment: (MopeduPaymentRequest) -> Unit,
    onAbandonPayment: (MopeduPaymentRequest) -> Unit,
    viewModel: RideHistoryViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    val opening = state.payment as? OutstandingPayment.OpeningSheet
    LaunchedEffect(opening) { opening?.let { onOpenPayment(it.request) } }
    DisposableEffect(Unit) {
        onDispose {
            val attempt = viewModel.activeAttempt()
            val current = viewModel.state.value.payment as? OutstandingPayment.OpeningSheet
            if (attempt != null && current != null && current.request.attempt == attempt) onAbandonPayment(current.request)
        }
    }

    UsScaffold(topBar = { UsTopBar(title = "Your rides", onBack = onBack) }) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            if (state.loading && state.rides.isEmpty()) {
                LoadingPane(modifier = Modifier.padding(padding))
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = padding,
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    item {
                        OutstandingSummary(
                            charges = state.outstanding,
                            totalPaise = state.outstandingTotalPaise,
                            onOpen = viewModel::openOutstanding,
                            modifier = Modifier.padding(top = UsTheme.spacing.m),
                        )
                    }
                    if (state.rides.isEmpty()) {
                        item { InfoNote("No rides yet. Your trips will show up here.") }
                    }
                    items(state.rides, key = { it.id }) { ride -> RideRow(ride) }
                }
            }
            state.error?.let { error ->
                MessageBanner(
                    message = UsMessage(error),
                    onDismiss = viewModel::dismissError,
                    modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = padding.calculateBottomPadding() + UsTheme.spacing.xxl),
                )
            }
        }
    }

    if (state.showOutstanding) {
        ModalBottomSheet(onDismissRequest = viewModel::closeOutstanding, containerColor = UsTheme.extended.bgCardSolid) {
            OutstandingSheet(
                charges = state.outstanding,
                payment = state.payment,
                onPay = { viewModel.pay(it) },
                onDone = viewModel::done,
            )
        }
    }
}

@Composable
private fun OutstandingSummary(charges: List<OutstandingCharge>, totalPaise: Long, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val pending = charges.filter { it.isPending }
    MopeduCard(modifier = modifier, onClick = onOpen, highlighted = pending.isNotEmpty()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(
                title = "Outstanding",
                body = if (pending.isEmpty()) "Nothing to pay." else "${pending.size} cancellation fee${if (pending.size > 1) "s" else ""} to settle",
                modifier = Modifier.weight(1f),
            )
            if (pending.isNotEmpty()) MopeduPill(MoneyPaise(totalPaise).formattedINR, PillTone.Warning)
        }
    }
}

@Composable
private fun RideRow(ride: RideBooking) {
    MopeduCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = ride.drop.address.ifBlank { "Ride" },
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = listOfNotNull(
                        ride.vehicleType.displayName,
                        formatDate(ride.completedAtEpochMs ?: ride.requestedAtEpochMs),
                    ).joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
            Column(horizontalAlignment = Alignment.End) {
                Text(
                    text = (ride.finalFare ?: ride.estimatedFare).formattedINR,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
                MopeduPill(statusLabel(ride.status), if (ride.status.isCancelled) PillTone.Warning else PillTone.Neutral)
            }
        }
    }
}

@Composable
private fun OutstandingSheet(
    charges: List<OutstandingCharge>,
    payment: OutstandingPayment,
    onPay: (String) -> Unit,
    onDone: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xxl)
            .padding(bottom = 32.dp),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        CardHeading("Outstanding", "Cancellation fees from earlier rides. Pay them here, or they are added to your next fare.")
        val pending = charges.filter { it.isPending }
        if (pending.isEmpty()) InfoNote("Nothing outstanding.", tone = PillTone.Positive)
        pending.forEach { charge ->
            val busy = payment.chargeIdOrNull() == charge.id
            MopeduCard {
                LabeledValue(charge.reason, charge.amount.formattedINR, emphasise = true)
                charge.rideId?.let { Text("Ride ${it.takeLast(RIDE_TAIL)}", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
                when (payment) {
                    is OutstandingPayment.CreatingIntent, is OutstandingPayment.OpeningSheet ->
                        if (busy) UsButton(text = "Opening payment…", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
                    is OutstandingPayment.Confirming ->
                        if (busy) UsButton(text = "Confirming…", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
                    is OutstandingPayment.Paid -> if (busy) InfoNote("Paid. Thank you.", tone = PillTone.Positive)
                    is OutstandingPayment.Failed -> if (busy) {
                        InfoNote(payment.reason ?: "That payment didn't go through.", tone = PillTone.Danger)
                        UsButton(text = "Try again", onClick = { onPay(charge.id) }, modifier = Modifier.fillMaxWidth())
                    }
                    is OutstandingPayment.StillConfirming -> if (busy) {
                        InfoNote("Still waiting for the bank. If you paid, it will clear shortly.", tone = PillTone.Warning)
                    }
                    OutstandingPayment.Idle -> Unit
                }
                if (!busy && payment is OutstandingPayment.Idle) {
                    UsButton(text = "Pay ${charge.amount.formattedINR}", onClick = { onPay(charge.id) }, modifier = Modifier.fillMaxWidth())
                }
            }
        }
        if (payment !is OutstandingPayment.Idle) {
            UsSecondaryButton(text = "Done", onClick = onDone, modifier = Modifier.fillMaxWidth())
        }
    }
}

private fun OutstandingPayment.chargeIdOrNull(): String? = when (this) {
    OutstandingPayment.Idle -> null
    is OutstandingPayment.CreatingIntent -> chargeId
    is OutstandingPayment.OpeningSheet -> chargeId
    is OutstandingPayment.Confirming -> chargeId
    is OutstandingPayment.Paid -> chargeId
    is OutstandingPayment.Failed -> chargeId
    is OutstandingPayment.StillConfirming -> chargeId
}

private fun statusLabel(status: RideStatus): String = when (status) {
    RideStatus.COMPLETED -> "Completed"
    RideStatus.CANCELLED_BY_CUSTOMER -> "Cancelled by you"
    RideStatus.CANCELLED_BY_PARTNER, RideStatus.CANCELLED_BY_ADMIN -> "Cancelled"
    RideStatus.EXPIRED, RideStatus.FAILED -> "No captain found"
    else -> status.code.replace('_', ' ').replaceFirstChar { it.uppercase() }
}

private val DATE_FORMAT = DateTimeFormatter.ofPattern("d MMM, h:mm a")

private fun formatDate(epochMs: Long): String? =
    epochMs.takeIf { it > 0 }?.let { DATE_FORMAT.format(Instant.ofEpochMilli(it).atZone(ZoneId.systemDefault())) }

private const val RIDE_TAIL = 6
