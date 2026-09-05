package com.us.android.feature.commerce.cart

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.CartLine
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice

/**
 * The cart.
 *
 * Two states here are not decoration. A line the server says exceeds
 * available stock, and a line whose price moved since it was added, both
 * BLOCK checkout — the first because the order would fail in the checkout
 * transaction, the second because charging a different number from the one on
 * screen is not something to resolve silently, however small the delta.
 */
@Composable
fun CartScreen(
    onBack: () -> Unit,
    onOpenProduct: (productId: String) -> Unit,
    onCheckout: () -> Unit,
    onContinueShopping: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: CartViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = "Cart", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            CartUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading cart",
            )

            CartUiState.Empty -> Column(
                modifier = Modifier
                    .padding(padding)
                    .fillMaxWidth(),
            ) {
                UsEmptyState(
                    title = "Your cart is empty",
                    detail = "Items you add will appear here.",
                )
            }

            is CartUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is CartUiState.Content -> CartContent(
                state = s,
                modifier = Modifier.padding(padding),
                onOpenProduct = onOpenProduct,
                onQuantityChange = viewModel::setQuantity,
                onRemove = viewModel::remove,
                onCheckout = onCheckout,
                onContinueShopping = onContinueShopping,
            )
        }
    }
}

@Composable
private fun CartContent(
    state: CartUiState.Content,
    modifier: Modifier,
    onOpenProduct: (String) -> Unit,
    onQuantityChange: (String, Int) -> Unit,
    onRemove: (String) -> Unit,
    onCheckout: () -> Unit,
    onContinueShopping: () -> Unit,
) {
    Column(modifier = modifier.fillMaxWidth()) {
        LazyColumn(
            modifier = Modifier.weight(1f),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            state.cart.sellerName?.takeIf { it.isNotBlank() }?.let { seller ->
                item {
                    Text(
                        text = "Sold by $seller",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textSecondary,
                    )
                }
            }

            if (state.unavailableLines.isNotEmpty()) {
                item {
                    CommerceNotice(
                        text = "Some items are no longer available in the quantity you chose. " +
                            "Reduce the quantity or remove them to continue.",
                    )
                }
            }
            if (state.repricedLines.isNotEmpty()) {
                item {
                    CommerceNotice(
                        text = "Prices changed for some items. The new price is shown below.",
                    )
                }
            }
            state.message?.let { message ->
                item { CommerceNotice(text = message) }
            }

            items(state.cart.items, key = { it.variantId }) { line ->
                CartRow(
                    line = line,
                    busy = line.variantId in state.busyVariantIds,
                    onOpen = { onOpenProduct(line.productId) },
                    onQuantityChange = { onQuantityChange(line.variantId, it) },
                    onRemove = { onRemove(line.variantId) },
                )
            }
        }

        CartFooter(
            state = state,
            onCheckout = onCheckout,
            onContinueShopping = onContinueShopping,
        )
    }
}

@Composable
@Suppress("LongMethod")
private fun CartRow(
    line: CartLine,
    busy: Boolean,
    onOpen: () -> Unit,
    onQuantityChange: (Int) -> Unit,
    onRemove: () -> Unit,
) {
    val overStocked = line.availableQty != null && line.quantity > line.availableQty!!
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        CommerceImage(
            url = line.imageUrl,
            contentDescription = line.title,
            modifier = Modifier
                .size(72.dp)
                .clickable(onClick = onOpen),
        )
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(
                text = line.title,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.clickable(onClick = onOpen),
            )

            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = line.unitPrice.formatWithSymbol(),
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                )
                // The old price, struck through, when the catalogue moved.
                // Showing only the new number would be the silent reprice
                // the checkout guard exists to prevent.
                line.priceChangedFrom?.let { was ->
                    Text(
                        text = was.formatWithSymbol(),
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textSecondary,
                        textDecoration = TextDecoration.LineThrough,
                    )
                }
            }

            if (overStocked) {
                Text(
                    text = "Only ${line.availableQty} left",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                QtyChip("−", enabled = !busy) { onQuantityChange(line.quantity - 1) }
                Text(
                    text = line.quantity.toString(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                )
                QtyChip(
                    "+",
                    enabled = !busy &&
                        (line.availableQty == null || line.quantity < line.availableQty!!),
                ) { onQuantityChange(line.quantity + 1) }

                Text(
                    text = "Remove",
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.textSecondary,
                    modifier = Modifier
                        .clickable(enabled = !busy, onClick = onRemove)
                        .padding(start = UsTheme.spacing.s),
                )
            }
        }

        Text(
            text = line.lineTotal.formatWithSymbol(),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
    }
}

@Composable
private fun QtyChip(label: String, enabled: Boolean, onClick: () -> Unit) {
    Text(
        text = label,
        style = MaterialTheme.typography.titleMedium,
        color = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textDim,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.bgCard)
            .clickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.xs),
    )
}

/**
 * Subtotal and the checkout button.
 *
 * The subtotal shown here is the server's cart subtotal. Delivery and GST are
 * deliberately absent: they are not known until the address is chosen and the
 * server prices the order, and showing a placeholder total that later changes
 * is exactly the surprise the price-change guard exists to prevent.
 */
@Composable
private fun CartFooter(
    state: CartUiState.Content,
    onCheckout: () -> Unit,
    onContinueShopping: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCard)
            .padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                text = "Subtotal",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Text(
                text = state.cart.subtotal.formatWithSymbol(),
                style = MaterialTheme.typography.titleMedium,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(
            text = "Delivery and GST are calculated at checkout.",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )
        UsButton(
            text = "Checkout",
            onClick = onCheckout,
            enabled = state.canCheckout,
            modifier = Modifier.fillMaxWidth(),
        )
        Text(
            text = "Continue shopping",
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier
                .clickable(onClick = onContinueShopping)
                .padding(vertical = UsTheme.spacing.xs),
        )
    }
}
