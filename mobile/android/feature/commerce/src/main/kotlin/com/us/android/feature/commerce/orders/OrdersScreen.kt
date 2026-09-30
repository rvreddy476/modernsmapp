package com.us.android.feature.commerce.orders

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Address
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderLine
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.PriceBreakdown
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.address.summary
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceProgressLine
import com.us.android.feature.commerce.ui.MStorePageBar
import com.us.android.feature.commerce.ui.PriceBreakdownCard
import com.us.android.feature.commerce.ui.pressScale

/**
 * Customer-facing status copy.
 *
 * Deliberately not `enum.name.lowercase()`. Two states need wording that says
 * what actually happened rather than naming an internal transition:
 * [OrderStatus.EXPIRED] must not imply the order is coming, and
 * [OrderStatus.REFUND_PENDING] must not read as "refunded".
 */
fun OrderStatus.label(): String = when (this) {
    OrderStatus.PAYMENT_PENDING -> "Awaiting payment"
    OrderStatus.PAYMENT_FAILED -> "Payment failed"
    OrderStatus.EXPIRED -> "Expired — items released"
    OrderStatus.CONFIRMED -> "Confirmed"
    OrderStatus.PACKED -> "Packed"
    OrderStatus.SHIPPED -> "Shipped"
    OrderStatus.OUT_FOR_DELIVERY -> "Out for delivery"
    OrderStatus.DELIVERED -> "Delivered"
    OrderStatus.CANCELLED -> "Cancelled"
    OrderStatus.REFUND_PENDING -> "Refund on the way"
    OrderStatus.REFUNDED -> "Refunded"
    // The server's vocabulary can grow ahead of a released app; an
    // unrecognised status must render as something neutral rather than crash
    // or claim a state we do not understand.
    OrderStatus.UNKNOWN -> "Updating"
}

fun PaymentStatus.label(): String = when (this) {
    PaymentStatus.PENDING -> "Payment pending"
    PaymentStatus.AWAITING_CONFIRMATION -> "Confirming payment"
    PaymentStatus.PAID -> "Paid"
    PaymentStatus.FAILED -> "Payment failed"
    PaymentStatus.REFUND_PENDING -> "Refund on the way"
    PaymentStatus.REFUNDED -> "Refunded"
    PaymentStatus.UNKNOWN -> "Updating"
}

/**
 * What an empty order list says.
 *
 * Deliberately different per scope: someone with a parcel in transit HAS
 * orders and just has no history, and telling them "no orders yet" on the
 * purchase-history page would be plainly wrong.
 */
internal fun emptyOrdersDetail(scope: OrderScope): String = when (scope) {
    OrderScope.ALL -> "Orders you place will appear here."
    OrderScope.PAST -> "Orders that have been delivered, cancelled or refunded will appear here."
}

@Composable
fun OrdersScreen(
    onBack: () -> Unit,
    onOpenOrder: (orderId: String) -> Unit,
    onStartShopping: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: OrdersViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = viewModel.scope.title, onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            OrdersUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading orders",
            )

            OrdersUiState.Empty -> Column(
                modifier = Modifier
                    .padding(padding)
                    .padding(UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                UsEmptyState(
                    title = when (viewModel.scope) {
                        OrderScope.ALL -> "No orders yet"
                        OrderScope.PAST -> "Nothing here yet"
                    },
                    detail = emptyOrdersDetail(viewModel.scope),
                )
                UsSecondaryButton(
                    text = "Start shopping",
                    onClick = onStartShopping,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            is OrdersUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is OrdersUiState.Content -> OrdersList(
                state = s,
                modifier = Modifier.padding(padding),
                onOpenOrder = onOpenOrder,
                onLoadMore = viewModel::loadMore,
            )
        }
    }
}

/**
 * The loaded orders, with the next page asked for as the end scrolls near
 * (2026-09-30) — from the list's own scroll position, the way the browse
 * grid does it, because the last row is composed before it is reachable.
 * One footer: the ember line while a page is in flight, the failure as one
 * line with a retry when it is not, nothing when there is no more.
 */
@Composable
private fun OrdersList(
    state: OrdersUiState.Content,
    modifier: Modifier = Modifier,
    onOpenOrder: (orderId: String) -> Unit,
    onLoadMore: () -> Unit,
) {
    val listState = rememberLazyListState()
    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index
                ?: return@derivedStateOf false
            state.hasMore && !state.loadingMore && state.loadMoreError == null &&
                last >= state.orders.size - PREFETCH_DISTANCE
        }
    }
    if (shouldLoadMore) onLoadMore()

    LazyColumn(
        state = listState,
        modifier = modifier.testTag("mstore_orders"),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        items(state.orders, key = { it.id }) { order ->
            OrderRow(order = order, onClick = { onOpenOrder(order.id) })
        }
        if (state.loadingMore) {
            item(key = "orders_loading_more") {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    CommerceProgressLine(contentDescription = "Loading more orders")
                }
            }
        }
        state.loadMoreError?.let { error ->
            item(key = "orders_load_more_failed") {
                Column(
                    modifier = Modifier.fillMaxWidth().testTag("mstore_orders_more_failed"),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                ) {
                    Text(
                        text = error,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textSecondary,
                    )
                    UsSecondaryButton(text = "Load more", onClick = onLoadMore)
                }
            }
        }
    }
}

/** How many rows before the end a page is asked for. */
private const val PREFETCH_DISTANCE = 4

@Composable
private fun OrderRow(order: Order, onClick: () -> Unit) {
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
                text = order.orderNumber,
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = order.breakdown.total.formatWithSymbol(),
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(
            text = order.status.label(),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
        // The list read sends no lines, only a count and the first title
        // (2026-09-30), so the row reads those rather than lines it never has.
        order.firstLineTitle?.let { first ->
            Text(
                text = if (order.lineCount > 1) {
                    "$first and ${order.lineCount - 1} more"
                } else {
                    first
                },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

// ─── Detail ──────────────────────────────────────────────────────────

/**
 * One order.
 *
 * [onOpenPaymentSheet] is `:app`'s hop onto the Activity for "Pay now": the
 * ViewModel mints the attempt (so a late ending for an earlier one is
 * ignored) and listens for the sheet's ending; the screen only hands the
 * attempt across.
 */
@Composable
fun OrderDetailScreen(
    onBack: () -> Unit,
    onOpenProduct: (productId: String) -> Unit,
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: OrderDetailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = "Order", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            OrderDetailUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading order",
            )

            is OrderDetailUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is OrderDetailUiState.Content -> {
                if (s.confirmingCancel) {
                    CancelOrderSheet(
                        onConfirm = { viewModel.cancel("Changed my mind") },
                        onDismiss = viewModel::dismissCancel,
                    )
                }
                OrderDetailBody(
                    state = s,
                    modifier = Modifier.padding(padding),
                    onOpenProduct = onOpenProduct,
                    onPayNow = {
                        viewModel.payNow()?.let { attempt -> onOpenPaymentSheet(attempt, s.order.orderNumber) }
                    },
                    onCancel = viewModel::askToCancel,
                )
            }
        }
    }
}

@Composable
@Suppress("LongMethod")
private fun OrderDetailBody(
    state: OrderDetailUiState.Content,
    modifier: Modifier,
    onOpenProduct: (String) -> Unit,
    onPayNow: () -> Unit,
    onCancel: () -> Unit,
) {
    val order = state.order
    LazyColumn(
        modifier = modifier.fillMaxWidth(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = order.orderNumber,
                    style = MaterialTheme.typography.titleMedium,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = order.status.label(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
                Text(
                    text = order.paymentStatus.label(),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }

        state.message?.let { item { CommerceNotice(text = it) } }

        // LB-22: an expired order released its stock. The copy must not imply
        // anything is coming, and a late capture is refunded rather than
        // fulfilled.
        if (order.status == OrderStatus.EXPIRED) {
            item {
                CommerceNotice(
                    text = "We held these items while you paid, but the hold ran out and " +
                        "they've gone back on sale. If any money was taken it's " +
                        "refunded automatically.",
                )
            }
        }

        // Awaiting payment, or failed and retryable (contract §4.7): the
        // intent route re-reserves the stock and re-opens the order, or
        // refuses with OUT_OF_STOCK, which lands in `message` above.
        if (order.canPayNow) {
            item {
                UsSecondaryButton(
                    text = if (state.paying) "Opening payment" else "Pay now",
                    onClick = onPayNow,
                    enabled = !state.paying,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("mstore_order_pay_now"),
                )
            }
        }

        items(order.lines, key = { it.variantId }) { line ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .pressScale(onClick = { onOpenProduct(line.productId) }),
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
                        text = "Qty ${line.quantity}",
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

        item {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                Text(
                    text = "Delivery address",
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    // LB-18: this is the snapshot taken at purchase, not the
                    // customer's current address book entry. Editing a saved
                    // address must never rewrite a past order's record.
                    text = order.deliveryAddress.summary(),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }

        item { PriceBreakdownCard(breakdown = order.breakdown) }

        order.trackingUrl?.takeIf { it.isNotBlank() }?.let { url ->
            item {
                Text(
                    text = "Tracking: $url",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }

        if (order.canCancel) {
            item {
                UsSecondaryButton(
                    text = "Cancel order",
                    onClick = onCancel,
                    enabled = !state.cancelling,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(bottom = UsTheme.spacing.xxl),
                )
            }
        }
    }
}

/**
 * Confirming a cancellation.
 *
 * A Momentum sheet, not Material's AlertDialog: every other confirmation in
 * the app — the screen-time nudge, the post menu's block and delete — comes
 * up from the bottom on the card surface, and a boxed dialog with two text
 * buttons in the middle of the screen is visibly from another product. It
 * also dismisses the way the rest of the app does, on scrim tap and Back.
 *
 * Both ways out are real buttons. A pair of look-alike text links is how
 * someone taps "Cancel order" meaning "cancel this dialog".
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CancelOrderSheet(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = MaterialTheme.colorScheme.scrim.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            SheetHandle()
            Text(
                text = "Cancel this order?",
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "If you've already paid, the refund starts automatically and can " +
                    "take a few days to reach your account.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            UsButton(
                text = "Cancel order",
                onClick = onConfirm,
                modifier = Modifier.fillMaxWidth(),
            )
            UsSecondaryButton(
                text = "Keep it",
                onClick = onDismiss,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

/** 32×4, muted at 35% — the same handle every Momentum sheet wears. */
@Composable
private fun SheetHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = HANDLE_TOP),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
        )
    }
}

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp

// ─── Previews ────────────────────────────────────────────────────────

@Suppress("MagicNumber")
internal fun previewOrder(
    status: OrderStatus = OrderStatus.PAYMENT_FAILED,
    paymentStatus: PaymentStatus = PaymentStatus.FAILED,
) = Order(
    id = "o-1",
    orderNumber = "MS-240912-0001",
    status = status,
    paymentStatus = paymentStatus,
    placedAtEpochSeconds = 0L,
    breakdown = PriceBreakdown(
        subtotal = Paise(199_900),
        discount = Paise.ZERO,
        shipping = Paise(4_000),
        tax = Paise(30_493),
        total = Paise(203_900),
    ),
    lines = listOf(
        OrderLine(
            productId = "p-1",
            variantId = "v-1",
            title = "Steel kettle, 1.7 L",
            imageMediaId = null,
            options = emptyList(),
            quantity = 1,
            unitPrice = Paise(199_900),
            lineTotal = Paise(199_900),
        ),
    ),
    deliveryAddress = Address(
        id = "a-1",
        label = "Home",
        contactName = "Asha",
        phone = "9800000000",
        line1 = "2 Test Road",
        line2 = null,
        landmark = null,
        city = "Bengaluru",
        state = "Karnataka",
        postalCode = "560001",
        isDefault = true,
    ),
    canCancel = true,
    trackingUrl = null,
)

@Preview(showBackground = true)
@Composable
private fun OrderDetailBodyPreview() {
    UsTheme {
        OrderDetailBody(
            state = OrderDetailUiState.Content(order = previewOrder()),
            modifier = Modifier,
            onOpenProduct = {},
            onPayNow = {},
            onCancel = {},
        )
    }
}

@Preview(showBackground = true)
@Composable
private fun OrdersListPreview() {
    UsTheme {
        OrdersList(
            state = OrdersUiState.Content(
                orders = listOf(
                    previewOrder(),
                    previewOrder(OrderStatus.SHIPPED, PaymentStatus.PAID).copy(id = "o-2", orderNumber = "MS-2"),
                ),
                nextCursor = "next",
                loadingMore = true,
            ),
            onOpenOrder = {},
            onLoadMore = {},
        )
    }
}
