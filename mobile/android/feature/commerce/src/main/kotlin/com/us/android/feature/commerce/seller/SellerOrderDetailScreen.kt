package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerAction
import com.us.android.core.commerce.model.SellerOrder
import com.us.android.core.commerce.model.SellerOrderLine
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.address.summary
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceSheet
import com.us.android.feature.commerce.ui.MSellerPageBar

/**
 * One order, from the seller's side.
 *
 * Reads top to bottom in the order a seller works: what to do (the action
 * buttons), what to pack (the lines), where it goes (the address), what it
 * is worth (the totals), and what has happened so far (the timeline).
 */
@Composable
fun SellerOrderDetailScreen(
    onBack: () -> Unit,
    viewModel: SellerOrderDetailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { MSellerPageBar(title = "Order", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            SellerOrderDetailUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading order",
            )

            is SellerOrderDetailUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is SellerOrderDetailUiState.Content -> {
                when (s.sheet) {
                    SellerOrderSheet.SHIP -> ShipSheet(state = s, viewModel = viewModel)
                    SellerOrderSheet.CANCEL -> CancelSheet(state = s, viewModel = viewModel)
                    null -> Unit
                }
                SellerOrderBody(
                    state = s,
                    padding = padding,
                    onPack = viewModel::pack,
                    onShip = viewModel::openShip,
                    onCancel = viewModel::openCancel,
                )
            }
        }
    }
}

@Composable
private fun SellerOrderBody(
    state: SellerOrderDetailUiState.Content,
    padding: PaddingValues,
    onPack: () -> Unit,
    onShip: () -> Unit,
    onCancel: () -> Unit,
) {
    val order = state.order
    LazyColumn(
        modifier = Modifier.padding(padding),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item { OrderHeader(order) }

        // Only shown while a sheet is NOT up; the sheet shows its own copy of
        // the same error, next to the field the seller has to fix.
        if (state.sheet == null) {
            state.error?.let { item { CommerceNotice(text = it) } }
        }

        if (state.actions.isNotEmpty()) {
            item {
                ActionButtons(
                    actions = state.actions,
                    busy = state.busy,
                    onPack = onPack,
                    onShip = onShip,
                    onCancel = onCancel,
                )
            }
        }

        item { SectionTitle("Items to pack") }
        items(order.lines, key = { it.itemId }) { line -> LineRow(line) }

        item {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                SectionTitle("Deliver to")
                val address = order.deliveryAddress
                Text(
                    text = address?.summary()?.trim()?.ifBlank { null }
                        ?: "The delivery address is not available on this order.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
                if (address != null && address.contactName.isBlank()) {
                    // Post PII cutover the seller card carries routing fields
                    // only. Say so, rather than leaving a seller to think the
                    // buyer forgot their name.
                    Text(
                        text = "The buyer's name and phone are on the courier label, not shown here.",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }
        }

        item { Totals(order) }

        order.shipment?.let { shipment ->
            item {
                Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                    SectionTitle("Shipment")
                    Text(
                        text = listOfNotNull(
                            shipment.courier.takeIf { it.isNotBlank() },
                            shipment.trackingNumber?.takeIf { it.isNotBlank() },
                        ).joinToString(" ").ifBlank { "Booked" },
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textPrimary,
                    )
                    shipment.trackingUrl?.takeIf { it.isNotBlank() }?.let {
                        Text(
                            text = it,
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textSecondary,
                        )
                    }
                }
            }
        }

        item {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                SectionTitle("Timeline")
                for (entry in state.timeline) {
                    TimelineRow(entry)
                }
            }
        }

        order.cancellationReason?.takeIf { it.isNotBlank() }?.let { reason ->
            item {
                CommerceNotice(
                    text = "Reason given: $reason",
                    modifier = Modifier.padding(bottom = UsTheme.spacing.xxl),
                )
            }
        }
    }
}

@Composable
private fun OrderHeader(order: SellerOrder) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Text(
            text = order.orderNumber.ifBlank { "Order" },
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = order.status.sellerLabel(),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        formatSellerTimestamp(order.placedAt)?.let { placed ->
            Text(
                text = "Placed $placed",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        order.paymentMethod?.takeIf { it.isNotBlank() }?.let { method ->
            Text(
                text = "Paid by ${method.uppercase()}",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

/**
 * The buttons, from the mirrored table.
 *
 * The primary action is whichever moves the parcel forward; cancel is
 * always the quiet one. All three disable together while any is in flight,
 * because the server holds one row and two transitions racing it produce a
 * refusal the seller did not cause.
 */
@Composable
private fun ActionButtons(
    actions: List<SellerAction>,
    busy: SellerAction?,
    onPack: () -> Unit,
    onShip: () -> Unit,
    onCancel: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        if (SellerAction.PACK in actions) {
            UsButton(
                text = "Mark as packed",
                onClick = onPack,
                enabled = busy == null,
                loading = busy == SellerAction.PACK,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (SellerAction.SHIP in actions) {
            UsButton(
                text = "Hand to courier",
                onClick = onShip,
                enabled = busy == null,
                loading = busy == SellerAction.SHIP,
                modifier = Modifier.fillMaxWidth(),
            )
        }
        if (SellerAction.CANCEL in actions) {
            UsSecondaryButton(
                text = "Cancel order",
                onClick = onCancel,
                enabled = busy == null,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun LineRow(line: SellerOrderLine) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        CommerceImage(
            url = line.imageUrl,
            contentDescription = line.title,
            modifier = Modifier.size(64.dp),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = line.title,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                // The SKU is what the seller picks from the shelf by.
                text = listOfNotNull(
                    "Qty ${line.quantity}",
                    line.sku.takeIf { it.isNotBlank() }?.let { "SKU $it" },
                ).joinToString("  "),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        Text(
            text = line.lineTotal.formatWithSymbol(),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/**
 * Two figures, both read from the server.
 *
 * The seller's share is what their lines came to; the order total is what
 * the buyer paid, which on a multi-seller order is more. Neither is derived
 * here from the other or from the lines.
 */
@Composable
private fun Totals(order: SellerOrder) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        SectionTitle("Totals")
        AmountLine("Your items", order.sellerSubtotal)
        AmountLine("Order total paid by buyer", order.orderTotal)
    }
}

@Composable
private fun AmountLine(label: String, amount: Paise) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = amount.formatWithSymbol(),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
    }
}

@Composable
private fun TimelineRow(entry: TimelineEntry) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(DOT)
                .clip(CircleShape)
                .background(
                    if (entry.done) UsTheme.extended.statusSuccess else UsTheme.extended.borderSubtle,
                ),
        )
        Text(
            text = entry.label,
            style = MaterialTheme.typography.bodyMedium,
            color = if (entry.done) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
            modifier = Modifier.weight(1f),
        )
        formatSellerTimestamp(entry.at)?.let { at ->
            Text(
                text = at,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleSmall,
        color = UsTheme.extended.textPrimary,
    )
}

/**
 * The ship sheet: courier and tracking number.
 *
 * Free text for the courier rather than a picker, because a small seller
 * uses whoever collects from their street and a list would be wrong for
 * most of them. The tracking number is checked for shape only; the
 * courier's site is the judge of whether it is real.
 */
@Composable
private fun ShipSheet(
    state: SellerOrderDetailUiState.Content,
    viewModel: SellerOrderDetailViewModel,
) {
    val form = state.shipForm
    val busy = state.busy == SellerAction.SHIP
    CommerceSheet(title = "Hand to courier", onDismiss = viewModel::dismissSheet) {
        Text(
            text = "The buyer follows this number on the courier's site, so copy it exactly " +
                "from the consignment slip.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        UsTextField(
            value = form.courier,
            onValueChange = { v -> viewModel.updateShipForm { it.copy(courier = v) } },
            label = "Courier",
            placeholder = "Delhivery, Blue Dart, India Post",
            enabled = !busy,
        )
        UsTextField(
            value = form.trackingNumber,
            onValueChange = { v -> viewModel.updateShipForm { it.copy(trackingNumber = v.uppercase()) } },
            label = "Tracking number",
            // Shown only once the seller has typed something, so an empty
            // sheet does not open covered in red.
            errorText = form.trackingProblem.takeIf { form.trackingNumber.isNotEmpty() },
            enabled = !busy,
        )
        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }
        UsButton(
            text = "Mark as shipped",
            onClick = viewModel::ship,
            enabled = state.canSubmitShip,
            loading = busy,
            modifier = Modifier.fillMaxWidth(),
        )
        UsSecondaryButton(
            text = "Not yet",
            onClick = viewModel::dismissSheet,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/**
 * The cancel sheet. A reason is required: the buyer reads it, and "cancelled
 * by seller" with no explanation is the message that turns into a dispute.
 */
@Composable
private fun CancelSheet(
    state: SellerOrderDetailUiState.Content,
    viewModel: SellerOrderDetailViewModel,
) {
    val busy = state.busy == SellerAction.CANCEL
    CommerceSheet(title = "Cancel this order?", onDismiss = viewModel::dismissSheet) {
        Text(
            text = "The buyer is told why, and if they have paid the refund starts automatically.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        UsTextField(
            value = state.cancelReason,
            onValueChange = viewModel::updateCancelReason,
            label = "Reason",
            placeholder = "Out of stock, damaged in storage",
            singleLine = false,
            enabled = !busy,
        )
        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }
        UsButton(
            text = "Cancel order",
            onClick = viewModel::cancel,
            enabled = state.canSubmitCancel,
            loading = busy,
            modifier = Modifier.fillMaxWidth(),
        )
        UsSecondaryButton(
            text = "Keep it",
            onClick = viewModel::dismissSheet,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

private val DOT = 10.dp
