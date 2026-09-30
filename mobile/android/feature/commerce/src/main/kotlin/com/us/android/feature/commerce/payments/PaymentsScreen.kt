package com.us.android.feature.commerce.payments

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.orders.appendOrders
import com.us.android.feature.commerce.orders.previewOrder
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceProgressLine
import com.us.android.feature.commerce.ui.MStorePageBar
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import com.us.android.feature.commerce.ui.pressScale
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * What MStore has charged, and how each charge stands.
 *
 * NOT a wallet. The app never holds or asks for card details — the PSP's own
 * sheet does that, and the server sources the publishable key — so "Payments"
 * here is the record of what was paid against which order, which is the
 * question a buyer opening this row actually has.
 *
 * A5: the launch is prepaid-only, and the page says so plainly rather than
 * leaving a buyer to discover it at checkout.
 */
sealed interface PaymentsUiState {
    data object Loading : PaymentsUiState
    data object Empty : PaymentsUiState

    /** The charges loaded so far; [nextCursor] null means all of them (2026-09-30). */
    data class Content(
        val orders: List<Order>,
        val nextCursor: String? = null,
        val loadingMore: Boolean = false,
        val loadMoreError: String? = null,
    ) : PaymentsUiState {
        val hasMore: Boolean get() = nextCursor != null
    }

    data class Failed(val message: String, val retryable: Boolean) : PaymentsUiState
}

@HiltViewModel
class PaymentsViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<PaymentsUiState>(PaymentsUiState.Loading)
    val state: StateFlow<PaymentsUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = PaymentsUiState.Loading
        viewModelScope.launch {
            when (val r = repo.orders()) {
                is CommerceResult.Failure -> _state.value = PaymentsUiState.Failed(
                    message = r.error.describe(),
                    retryable = r.error.isRetryable(),
                )

                is CommerceResult.Success -> {
                    _state.value = if (r.value.items.isEmpty() && r.value.nextCursor == null) {
                        PaymentsUiState.Empty
                    } else {
                        PaymentsUiState.Content(r.value.items, nextCursor = r.value.nextCursor)
                    }
                }
            }
        }
    }

    /** The next page of charges, appended; a no-op while one is in flight or at the end. */
    fun loadMore() {
        val current = _state.value as? PaymentsUiState.Content ?: return
        val cursor = current.nextCursor ?: return
        if (current.loadingMore) return

        _state.value = current.copy(loadingMore = true, loadMoreError = null)
        viewModelScope.launch {
            val latest = _state.value as? PaymentsUiState.Content ?: return@launch
            _state.value = when (val r = repo.orders(cursor)) {
                is CommerceResult.Failure -> latest.copy(loadingMore = false, loadMoreError = r.error.describe())
                is CommerceResult.Success -> latest.copy(
                    orders = appendOrders(latest.orders, r.value.items),
                    nextCursor = r.value.nextCursor,
                    loadingMore = false,
                )
            }
        }
    }
}

@Composable
fun PaymentsScreen(
    onBack: () -> Unit,
    onOpenOrder: (orderId: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: PaymentsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = "Payments", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            PaymentsUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading payments",
            )

            PaymentsUiState.Empty -> Column(
                modifier = Modifier.padding(padding).padding(UsTheme.spacing.pageHorizontal),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                UsEmptyState(
                    title = "No payments yet",
                    detail = "What you pay for an order shows up here.",
                )
                CommerceNotice(text = PREPAID_NOTICE)
            }

            is PaymentsUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is PaymentsUiState.Content -> PaymentsList(
                state = s,
                modifier = Modifier.padding(padding),
                onOpenOrder = onOpenOrder,
                onLoadMore = viewModel::loadMore,
            )
        }
    }
}

/** The charges, paged from the list's own scroll position like the orders list. */
@Composable
private fun PaymentsList(
    state: PaymentsUiState.Content,
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
        modifier = modifier.testTag("mstore_payments"),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        item { CommerceNotice(text = PREPAID_NOTICE) }
        items(state.orders, key = { it.id }) { order ->
            PaymentRow(order = order, onClick = { onOpenOrder(order.id) })
        }
        if (state.loadingMore) {
            item(key = "payments_loading_more") {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    CommerceProgressLine(contentDescription = "Loading more payments")
                }
            }
        }
        state.loadMoreError?.let { error ->
            item(key = "payments_load_more_failed") {
                Column(
                    modifier = Modifier.fillMaxWidth(),
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

private const val PREFETCH_DISTANCE = 4

/**
 * One charge: the order it belongs to, what it came to, and where it stands.
 *
 * The status is the SERVER's payment status, not an inference from the order
 * state — A1: a redirect is evidence, never proof, and this row must never say
 * "Paid" for something a webhook has not confirmed.
 */
@Composable
private fun PaymentRow(order: Order, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick, role = Role.Button)
            .padding(vertical = UsTheme.spacing.m)
            .testTag("payment_row:${order.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                text = order.orderNumber,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = order.breakdown.total.formatWithSymbol(),
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(
            text = order.paymentStatus.paymentLabel(),
            style = MaterialTheme.typography.labelMedium,
            color = when (order.paymentStatus) {
                PaymentStatus.PAID -> UsTheme.extended.statusSuccess
                PaymentStatus.FAILED -> UsTheme.extended.statusDanger
                else -> UsTheme.extended.textSecondary
            },
        )
    }
}

/** The record's own wording, distinct from the order list's. */
fun PaymentStatus.paymentLabel(): String = when (this) {
    PaymentStatus.PENDING -> "Not paid yet"

    // A1: the sheet came back and looked genuine, and that is all we know.
    PaymentStatus.AWAITING_CONFIRMATION -> "Confirming with your bank"
    PaymentStatus.PAID -> "Paid"
    PaymentStatus.FAILED -> "Payment failed"
    PaymentStatus.REFUND_PENDING -> "Refund on the way"
    PaymentStatus.REFUNDED -> "Refunded"
    PaymentStatus.UNKNOWN -> "Updating"
}

private const val PREPAID_NOTICE =
    "Orders are paid up front by UPI or card. Cash on delivery is not available yet, and " +
        "MStore never stores your card — your bank's own sheet takes the payment."

@Preview(showBackground = true)
@Composable
private fun PaymentsListPreview() {
    UsTheme {
        PaymentsList(
            state = PaymentsUiState.Content(
                orders = listOf(
                    previewOrder(),
                    previewOrder(OrderStatus.CONFIRMED, PaymentStatus.PAID).copy(id = "o-2", orderNumber = "MS-2"),
                ),
                nextCursor = "next",
                loadingMore = true,
            ),
            onOpenOrder = {},
            onLoadMore = {},
        )
    }
}
