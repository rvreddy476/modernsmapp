package com.us.android.feature.feast.tracking

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.toRupeeText
import com.us.android.feature.feast.cart.BillCard
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.FoodTypeMark
import com.us.android.feature.feast.ui.InfoNote
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.MoneyRow
import com.us.android.feature.feast.ui.Pill
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone
import com.us.android.feature.feast.ui.distanceText
import com.us.android.feature.feast.ui.etaText
import com.us.android.feature.feast.ui.formatPlacedAt
import com.us.android.feature.feast.ui.serverTotal
import com.us.android.feature.feast.ui.updatedText
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun OrderTrackingScreen(
    onBack: () -> Unit,
    onOpenInvoice: (String) -> Unit,
    viewModel: OrderTrackingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var now by remember { mutableStateOf(Instant.now()) }
    LaunchedEffect(Unit) {
        while (true) {
            now = Instant.now()
            delay(1_000)
        }
    }
    var confirmCancel by rememberSaveable { mutableStateOf(false) }
    if (confirmCancel) {
        AlertDialog(
            onDismissRequest = { confirmCancel = false },
            title = { Text("Cancel this order?") },
            text = { Text("If you've paid, the refund goes back to your original payment method.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmCancel = false
                    viewModel.cancel()
                }) { Text("Cancel order", color = UsTheme.extended.statusDanger) }
            },
            dismissButton = { TextButton(onClick = { confirmCancel = false }) { Text("Keep order", color = UsTheme.extended.textMuted) } },
            containerColor = UsTheme.extended.bgRaised,
            titleContentColor = UsTheme.extended.textPrimary,
            textContentColor = UsTheme.extended.textSecondary,
        )
    }

    FeastScreen(
        title = state.order?.orderNumber?.let { "Order $it" } ?: "Your order",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        val order = state.order
        val timeline = state.timeline
        when {
            state.loading -> LoadingPane()
            order == null || timeline == null -> MessagePane(
                title = "Couldn't load this order",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::refreshNow,
            )
            else -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 32.dp),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                val model = state.model
                // Status and ETA.
                FeastCard {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            text = if (timeline.cancelled) timeline.cancelledText.orEmpty() else timeline.steps[timeline.currentIndex].title,
                            style = MaterialTheme.typography.headlineSmall.copy(fontFamily = MomentumWordmarkFontFamily),
                            color = UsTheme.extended.textPrimary,
                            modifier = Modifier.weight(1f),
                        )
                        if (OrderTimeline.isLive(order.status)) LiveSignal(state.live)
                    }
                    val eta = etaText(model.etaAt, now)
                        ?: order.estimatedDeliveryMinutes.takeIf { it > 0 && OrderTimeline.isLive(order.status) }?.let { "About $it min" }
                    eta?.let { Text(it, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.accentSolid) }
                    val rider = model.rider
                    val customerLat = model.customerLatitude
                    val customerLng = model.customerLongitude
                    if (rider != null && customerLat != null && customerLng != null && timeline.steps[timeline.currentIndex] == TimelineStep.ON_THE_WAY) {
                        Text(
                            "Your delivery partner is ${distanceText(distanceKm(rider.latitude, rider.longitude, customerLat, customerLng))}",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textSecondary,
                        )
                    }
                    updatedText(model.lastUpdatedAt, now)?.let {
                        Text(it, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textDim)
                    }
                }

                if (OrderTimeline.isLive(order.status)) {
                    LocalMapSurface.current.Content(
                        rider = model.rider?.let { MapPoint(it.latitude, it.longitude) },
                        destination = if (customerPoint(model) != null) customerPoint(model) else null,
                        modifier = Modifier,
                    )
                }

                // The delivery code, only while the rider has the food.
                state.deliveryCode?.let { code -> DeliveryCodeCard(code) }

                SectionLabel("Progress")
                FeastCard { Timeline(timeline) }

                SectionLabel("${order.restaurantName} · ${formatPlacedAt(order.placedAt)}")
                FeastCard {
                    order.items.forEach { line ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            FoodTypeMark(line.foodType)
                            Spacer(Modifier.width(UsTheme.spacing.m))
                            Text("${line.quantity} × ${line.name}", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary, modifier = Modifier.weight(1f))
                            Text(line.lineTotalPaise.toRupeeText(), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                        }
                    }
                    if (state.bill?.total == null) MoneyRow("Total", order.serverTotal(), emphasise = true)
                }
                state.bill?.takeIf { it.total != null }?.let { BillCard(it, title = "Bill") }

                UsSecondaryButton(text = "View invoice", onClick = { onOpenInvoice(order.id) }, modifier = Modifier.fillMaxWidth())
                if (state.canCancel) {
                    UsSecondaryButton(
                        text = if (state.cancelling) "Cancelling…" else "Cancel order",
                        onClick = { confirmCancel = true },
                        enabled = !state.cancelling,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
    }
}

private fun customerPoint(model: TrackingModel): MapPoint? {
    val lat = model.customerLatitude ?: return null
    val lng = model.customerLongitude ?: return null
    return MapPoint(lat, lng)
}

@Composable
private fun LiveSignal(live: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier
                .size(8.dp)
                .background(if (live) UsTheme.extended.liveRed else UsTheme.extended.textDim, CircleShape),
        )
        Spacer(Modifier.width(UsTheme.spacing.s))
        Text(if (live) "LIVE" else "Refreshing", style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
    }
}

@Composable
private fun DeliveryCodeCard(code: String) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCardSolid, shape)
            .border(1.5.dp, UsTheme.extended.accentSolid, shape)
            .padding(UsTheme.spacing.xxl)
            .semantics { contentDescription = "Delivery code ${code.toList().joinToString(" ")}" },
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text("Delivery code", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textMuted)
        Text(
            text = code.toList().joinToString("  "),
            style = MaterialTheme.typography.displaySmall.copy(fontSize = 40.sp, letterSpacing = 4.sp),
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.padding(vertical = UsTheme.spacing.m),
        )
        Text(
            "Show this to your delivery partner when your food arrives. Never share it before then.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
    }
}

@Composable
private fun Timeline(view: TimelineView) {
    Column {
        view.steps.forEachIndexed { index, step ->
            val done = index < view.currentIndex || (index == view.currentIndex && step == TimelineStep.DELIVERED)
            val current = index == view.currentIndex && !view.cancelled && step != TimelineStep.DELIVERED
            val color = when {
                view.cancelled && index > view.currentIndex -> UsTheme.extended.textGhost
                done -> UsTheme.extended.statusSuccess
                current -> UsTheme.extended.accentSolid
                else -> UsTheme.extended.textDim
            }
            Row {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Box(Modifier.size(12.dp).background(color, CircleShape))
                    if (index < view.steps.lastIndex) {
                        Box(Modifier.width(2.dp).height(34.dp).background(if (done) UsTheme.extended.statusSuccess else UsTheme.extended.borderSubtle))
                    }
                }
                Spacer(Modifier.width(UsTheme.spacing.l))
                Column(Modifier.padding(bottom = UsTheme.spacing.m)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            step.title,
                            style = MaterialTheme.typography.titleSmall,
                            color = if (done || current) UsTheme.extended.textPrimary else UsTheme.extended.textDim,
                        )
                        if (current) {
                            Spacer(Modifier.width(UsTheme.spacing.m))
                            Pill("Now", Tone.Accent)
                        }
                    }
                    if (current) Text(step.detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                }
            }
        }
        if (view.cancelled) InfoNote(view.cancelledText.orEmpty(), tone = Tone.Danger)
    }
}
