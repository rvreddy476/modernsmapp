package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.SellerOrderSummary
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar
import com.us.android.feature.commerce.ui.pressScale

/**
 * Seller-facing status copy.
 *
 * Not the buyer's labels: "Awaiting payment" tells a buyer to pay, but tells
 * a seller nothing to do. Each line here says where the parcel is from the
 * shop's side of the counter.
 */
fun OrderStatus.sellerLabel(): String = when (this) {
    OrderStatus.PAYMENT_PENDING -> "Buyer has not paid yet"
    OrderStatus.PAYMENT_FAILED -> "Payment failed"
    OrderStatus.EXPIRED -> "Expired unpaid"
    OrderStatus.CONFIRMED -> "Paid, ready to pack"
    OrderStatus.PACKED -> "Packed, ready to ship"
    OrderStatus.SHIPPED -> "With the courier"
    OrderStatus.OUT_FOR_DELIVERY -> "Out for delivery"
    OrderStatus.DELIVERED -> "Delivered"
    OrderStatus.CANCELLED -> "Cancelled"
    OrderStatus.REFUND_PENDING -> "Cancelled, refund on the way"
    OrderStatus.REFUNDED -> "Refunded"
    OrderStatus.UNKNOWN -> "Updating"
}

@Composable
fun SellerOrdersScreen(
    onBack: () -> Unit,
    onOpenOrder: (orderId: String) -> Unit,
    viewModel: SellerOrdersViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { MSellerPageBar(title = "Orders", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            SellerOrdersUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading orders",
            )

            is SellerOrdersUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is SellerOrdersUiState.Content -> SellerOrdersContent(
                state = s,
                padding = padding,
                onSelect = viewModel::select,
                onLoadMore = viewModel::loadMore,
                onOpenOrder = onOpenOrder,
            )
        }
    }
}

@Composable
private fun SellerOrdersContent(
    state: SellerOrdersUiState.Content,
    padding: PaddingValues,
    onSelect: (SellerOrderFilter) -> Unit,
    onLoadMore: () -> Unit,
    onOpenOrder: (String) -> Unit,
) {
    LazyColumn(
        modifier = Modifier.padding(padding),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        item { FilterChips(selected = state.filter, onSelect = onSelect) }

        state.message?.let { item { CommerceNotice(text = it) } }

        val visible = state.visible
        if (visible.isEmpty()) {
            item {
                UsEmptyState(
                    title = if (state.orders.isEmpty()) "No orders yet" else "Nothing here",
                    detail = if (state.orders.isEmpty()) {
                        "Orders buyers place with your shop will appear here."
                    } else {
                        "No order matches this filter yet."
                    },
                    modifier = Modifier.padding(vertical = UsTheme.spacing.xxl),
                )
            }
        } else {
            items(visible, key = { it.id }) { order ->
                SellerOrderRow(order = order, onClick = { onOpenOrder(order.id) })
            }
        }

        // A button, not an infinite scroll: an offset-paged list that loads
        // while the seller reads it can repeat a row when an order lands
        // between pages, and a tap makes that moment the seller's choice.
        if (!state.exhausted) {
            item {
                UsSecondaryButton(
                    text = if (state.loadingMore) "Loading" else "Load more",
                    onClick = onLoadMore,
                    enabled = state.canLoadMore,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(bottom = UsTheme.spacing.xxl),
                )
            }
        }
    }
}

@Composable
private fun FilterChips(
    selected: SellerOrderFilter,
    onSelect: (SellerOrderFilter) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        for (filter in SellerOrderFilter.entries) {
            UsPillButton(
                text = filter.label,
                onClick = { onSelect(filter) },
                filled = filter == selected,
            )
        }
    }
}

@Composable
private fun SellerOrderRow(order: SellerOrderSummary, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .pressScale(onClick = onClick)
            .padding(UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                text = order.orderNumber.ifBlank { "Order" },
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = order.total.formatWithSymbol(),
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(
            text = order.status.sellerLabel(),
            style = MaterialTheme.typography.bodySmall,
            // The two statuses that need a hand are the two that stand out.
            color = if (order.status == OrderStatus.CONFIRMED || order.status == OrderStatus.PACKED) {
                UsTheme.extended.textPrimary
            } else {
                UsTheme.extended.textSecondary
            },
        )
        // The seller's own share of the order, when the server says. On a
        // multi-seller order the header's total is the buyer's whole bill,
        // and this line is the number the seller is actually reconciling.
        if (order.itemCount > 0) {
            val items = if (order.itemCount == 1) "1 item" else "${order.itemCount} items"
            Text(
                text = "$items, your share ${order.sellerSubtotal.formatWithSymbol()}",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        formatSellerTimestamp(order.placedAt)?.let { placed ->
            Text(
                text = "Placed $placed",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}
