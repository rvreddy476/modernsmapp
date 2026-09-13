package com.us.android.feature.kitchen.queue

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.feature.kitchen.money.RupeeFormat
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LabeledValue
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.MessagePane
import com.us.android.feature.kitchen.ui.OrderStatusText
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.humanise
import com.us.android.feature.kitchen.ui.listPadding

@Composable
fun OrderDetailScreen(onBack: () -> Unit, viewModel: OrderDetailViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val order = state.order
    KitchenScreen(
        title = order?.let { "Order #${it.orderNumber}" } ?: "Order",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        when {
            order == null && state.loading -> LoadingPane(Modifier.padding(padding))
            order == null -> MessagePane(
                title = "Order not found",
                body = state.loadError.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
                modifier = Modifier.padding(padding),
            )
            else -> OrderDetailContent(
                order = order,
                state = state,
                padding = padding,
                onMarkReady = viewModel::markReady,
                onPickupCode = viewModel::onPickupCode,
                onVerifyPickup = viewModel::verifyPickup,
            )
        }
    }
}

@Composable
private fun OrderDetailContent(
    order: PartnerOrderDto,
    state: OrderDetailUiState,
    padding: androidx.compose.foundation.layout.PaddingValues,
    onMarkReady: () -> Unit,
    onPickupCode: (String) -> Unit,
    onVerifyPickup: () -> Unit,
) {
    LazyColumn(
        contentPadding = listPadding(padding),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item(key = "status") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                KitchenPill(OrderStatusText.label(order.status), OrderStatusText.tone(order.status))
                Text(
                    text = order.placedAt?.take(16)?.replace('T', ' ')?.let { "Placed $it" }.orEmpty(),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(start = UsTheme.spacing.l),
                )
            }
        }
        if (order.status == OrderStatusText.PREPARING) {
            item(key = "ready") {
                UsButton(
                    text = "Mark ready for pickup",
                    onClick = onMarkReady,
                    loading = state.markingReady,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        if (order.status in OrderStatusText.AWAITING_PICKUP) {
            item(key = "pickup") {
                KitchenCard {
                    CardHeading(
                        title = "Hand over to the rider",
                        body = "Ask the rider for their pickup code. The order is marked picked up only when the code matches.",
                    )
                    UsTextField(
                        value = state.pickupCode,
                        onValueChange = onPickupCode,
                        label = "Pickup code",
                        errorText = state.pickupError,
                        keyboardType = KeyboardType.Number,
                    )
                    UsButton(
                        text = "Confirm handover",
                        onClick = onVerifyPickup,
                        loading = state.verifying,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }

        item(key = "h-items") { SectionHeader("Items") }
        item(key = "items") {
            KitchenCard {
                if (order.items.isEmpty()) {
                    Text(
                        text = "Item details aren't available for this order.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textMuted,
                    )
                }
                order.items.forEach { line ->
                    Row(verticalAlignment = Alignment.Top) {
                        Text(
                            text = "${line.quantity} ×",
                            style = MaterialTheme.typography.titleSmall,
                            color = UsTheme.extended.accentSolid,
                            modifier = Modifier.width(40.dp),
                        )
                        Column(modifier = Modifier.weight(1f)) {
                            Text(line.name, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                            line.instruction?.takeIf { it.isNotBlank() }?.let {
                                Text("“$it”", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                            }
                        }
                        Text(
                            text = RupeeFormat.format(line.lineTotalPaise),
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textSecondary,
                        )
                    }
                }
            }
        }

        item(key = "h-bill") { SectionHeader("Bill") }
        item(key = "bill") {
            val totals = order.totals
            KitchenCard {
                LabeledValue("Items", RupeeFormat.format(totals.itemSubtotal))
                if (totals.addonTotal.value > 0) LabeledValue("Add-ons", RupeeFormat.format(totals.addonTotal))
                if (totals.packagingFee.value > 0) LabeledValue("Packaging", RupeeFormat.format(totals.packagingFee))
                LabeledValue("Taxes", RupeeFormat.format(totals.taxTotal))
                if (totals.restaurantDiscount.value > 0) {
                    LabeledValue("Your discount", "-${RupeeFormat.format(totals.restaurantDiscount)}")
                }
                HorizontalDivider(color = UsTheme.extended.borderSubtle)
                LabeledValue("Order total", RupeeFormat.format(totals.finalAmount), emphasise = true)
                if (order.paymentMethod.isNotBlank()) {
                    LabeledValue("Payment", "${humanise(order.paymentMethod)} · ${humanise(order.paymentStatus)}")
                }
            }
        }

        if (order.history.isNotEmpty()) {
            item(key = "h-history") { SectionHeader("Timeline") }
            items(order.history) { step ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = OrderStatusText.label(step.toStatus),
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.weight(1f),
                    )
                    Text(
                        text = step.createdAt?.take(16)?.replace('T', ' ').orEmpty(),
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }
        }
    }
}
