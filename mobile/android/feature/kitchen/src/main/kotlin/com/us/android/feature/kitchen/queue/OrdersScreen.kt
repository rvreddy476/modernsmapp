package com.us.android.feature.kitchen.queue

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.feature.kitchen.money.RupeeFormat
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.OrderStatusText
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.RestaurantStatusText
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.listPadding
import java.util.Locale

@Composable
fun OrdersScreen(
    restaurant: PartnerRestaurantDto,
    onOpenOrder: (orderId: String) -> Unit,
    onOpenKitchenTab: () -> Unit,
    bottomBar: @Composable () -> Unit,
    viewModel: OrderQueueViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var rejecting by rememberSaveable { mutableStateOf<String?>(null) }

    val context = LocalContext.current
    var alertsAllowed by remember {
        mutableStateOf(
            Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
                ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED,
        )
    }
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        alertsAllowed = granted
    }

    KitchenScreen(
        title = "Orders",
        onBack = null,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = bottomBar,
        actions = { TransportPill(state.transport, Modifier.padding(end = UsTheme.spacing.l)) },
    ) { padding ->
        if (!state.loaded) {
            LoadingPane(Modifier.padding(padding))
            return@KitchenScreen
        }
        LazyColumn(
            contentPadding = listPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            if (!alertsAllowed) {
                item(key = "alerts") {
                    // The explanation IS the rationale: the system prompt only
                    // opens from this card's button.
                    KitchenCard {
                        CardHeading(
                            title = "Turn on new-order alerts",
                            body = "So a new order rings even when Feast Kitchen is in the background.",
                        )
                        UsPillButton(
                            text = "Turn on alerts",
                            onClick = { notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS) },
                        )
                    }
                }
            }
            if (restaurant.status != RestaurantStatusText.ACTIVE) {
                item(key = "not-live") {
                    KitchenCard(onClick = onOpenKitchenTab) {
                        CardHeading(
                            title = "Your kitchen isn't live yet",
                            body = "${RestaurantStatusText.label(restaurant.status)}. Orders arrive once Feast approves your kitchen.",
                            titleColor = UsTheme.extended.statusWarning,
                        )
                    }
                }
            } else if (!restaurant.isAcceptingOrders) {
                item(key = "paused") {
                    KitchenCard(onClick = onOpenKitchenTab) {
                        CardHeading(
                            title = "You're not taking orders",
                            body = "Customers can't order right now. Turn orders back on in the Kitchen tab.",
                            titleColor = UsTheme.extended.statusWarning,
                        )
                    }
                }
            }
            state.error?.let { error -> item(key = "error") { InfoNote(error, tone = PillTone.Danger) } }

            item(key = "h-new") {
                SectionHeader(
                    title = "New orders",
                    trailing = { if (state.incoming.isNotEmpty()) KitchenPill("${state.incoming.size}", PillTone.Accent) },
                )
            }
            if (state.incoming.isEmpty()) {
                item(key = "e-new") { EmptyLine("No new orders. When one arrives it appears here, with an alert.") }
            }
            items(state.incoming, key = { "in-${it.order.id}" }) { ui ->
                IncomingOrderCard(
                    ui = ui,
                    busy = ui.order.id in state.busyOrderIds,
                    onAccept = { viewModel.accept(ui.order.id) },
                    onReject = { rejecting = ui.order.id },
                )
            }

            item(key = "h-kitchen") { SectionHeader("In the kitchen") }
            if (state.preparing.isEmpty()) item(key = "e-kitchen") { EmptyLine("Nothing cooking right now.") }
            items(state.preparing, key = { "pr-${it.id}" }) { order ->
                ActiveOrderCard(
                    order = order,
                    busy = order.id in state.busyOrderIds,
                    actionLabel = "Mark ready",
                    onAction = { viewModel.markReady(order.id) },
                    onOpen = { onOpenOrder(order.id) },
                )
            }

            item(key = "h-pickup") { SectionHeader("Waiting for a rider") }
            if (state.awaitingPickup.isEmpty()) item(key = "e-pickup") { EmptyLine("No orders waiting for pickup.") }
            items(state.awaitingPickup, key = { "pu-${it.id}" }) { order ->
                ActiveOrderCard(
                    order = order,
                    busy = false,
                    actionLabel = "Enter pickup code",
                    onAction = { onOpenOrder(order.id) },
                    onOpen = { onOpenOrder(order.id) },
                )
            }
        }
    }

    rejecting?.let { orderId ->
        RejectDialog(
            onReject = { reason ->
                viewModel.reject(orderId, reason)
                rejecting = null
            },
            onDismiss = { rejecting = null },
        )
    }
}

@Composable
private fun IncomingOrderCard(ui: IncomingOrderUi, busy: Boolean, onAccept: () -> Unit, onReject: () -> Unit) {
    val order = ui.order
    val window = ui.window
    KitchenCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = "#${order.orderNumber}",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = "${order.itemCount} ${if (order.itemCount == 1) "item" else "items"} · ${RupeeFormat.format(order.finalAmount)}",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
            }
            CountdownPill(window)
        }
        order.customerInstruction?.takeIf { it.isNotBlank() }?.let { InfoNote("From the customer: $it") }
        when {
            window == AcceptWindow.Expired -> InfoNote(
                text = "Missed. This order will be rejected automatically and a paid order refunded.",
                tone = PillTone.Danger,
            )
            window.canRespond -> Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                UsSecondaryButton(text = "Reject", onClick = onReject, enabled = !busy, modifier = Modifier.weight(1f))
                UsButton(text = "Accept", onClick = onAccept, loading = busy, modifier = Modifier.weight(1f))
            }
            else -> Unit
        }
    }
}

@Composable
private fun CountdownPill(window: AcceptWindow) {
    when (window) {
        is AcceptWindow.Open -> KitchenPill(
            text = formatCountdown(window.remainingSeconds),
            tone = if (window.urgent) PillTone.Danger else PillTone.Accent,
        )
        AcceptWindow.NoDeadline -> KitchenPill("New", PillTone.Accent)
        AcceptWindow.Expired -> KitchenPill("Expired", PillTone.Danger)
        AcceptWindow.Accepted -> KitchenPill("Accepted", PillTone.Positive)
        AcceptWindow.Rejected -> KitchenPill("Rejected", PillTone.Neutral)
    }
}

@Composable
private fun ActiveOrderCard(
    order: PartnerOrderDto,
    busy: Boolean,
    actionLabel: String,
    onAction: () -> Unit,
    onOpen: () -> Unit,
) {
    KitchenCard(onClick = onOpen) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "#${order.orderNumber}",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            KitchenPill(OrderStatusText.label(order.status), OrderStatusText.tone(order.status))
        }
        Text(
            text = order.items.joinToString(" · ") { "${it.quantity} × ${it.name}" }.ifBlank { "Open to see the items" },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        UsPillButton(text = actionLabel, onClick = onAction, busy = busy)
    }
}

@Composable
private fun TransportPill(transport: QueueTransport, modifier: Modifier = Modifier) {
    when (transport) {
        QueueTransport.LIVE -> KitchenPill("Live", PillTone.Positive, modifier)
        QueueTransport.POLLING -> KitchenPill("Every 15 s", PillTone.Warning, modifier)
        QueueTransport.CONNECTING -> KitchenPill("Connecting", PillTone.Neutral, modifier)
    }
}

@Composable
private fun EmptyLine(text: String) {
    Text(text = text, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
}

@Composable
private fun RejectDialog(onReject: (String) -> Unit, onDismiss: () -> Unit) {
    val reasons = listOf("An item is out of stock", "The kitchen is too busy", "We're closing soon")
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Reject this order?") },
        text = {
            Column {
                Text(
                    text = "A paid order is refunded to the customer. Choose a reason:",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
                reasons.forEach { reason ->
                    Text(
                        text = reason,
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onReject(reason) }
                            .padding(vertical = 12.dp),
                    )
                }
            }
        },
        confirmButton = {},
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Keep order", color = UsTheme.extended.accentSolid) }
        },
        containerColor = UsTheme.extended.bgRaised,
        titleContentColor = UsTheme.extended.textPrimary,
    )
}

internal fun formatCountdown(seconds: Long): String =
    String.format(Locale.US, "%d:%02d", seconds / SECONDS_PER_MINUTE, seconds % SECONDS_PER_MINUTE)

private const val SECONDS_PER_MINUTE = 60L
