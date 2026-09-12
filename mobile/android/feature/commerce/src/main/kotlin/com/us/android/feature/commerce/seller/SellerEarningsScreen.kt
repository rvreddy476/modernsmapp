package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerEarning
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar

/**
 * What the seller has earned on delivered prepaid orders.
 *
 * COD orders are settled through a separate remittance ledger and are not
 * on this page; the empty state says so, because a seller whose sales are
 * mostly cash would otherwise read an empty list as "not paid".
 */
@Composable
fun SellerEarningsScreen(
    onBack: () -> Unit,
    onOpenOrder: (orderId: String) -> Unit,
    viewModel: SellerEarningsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { MSellerPageBar(title = "Earnings", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            SellerEarningsUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading earnings",
            )

            is SellerEarningsUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is SellerEarningsUiState.Content -> EarningsContent(
                state = s,
                padding = padding,
                onLoadMore = viewModel::loadMore,
                onOpenOrder = onOpenOrder,
            )
        }
    }
}

@Composable
private fun EarningsContent(
    state: SellerEarningsUiState.Content,
    padding: PaddingValues,
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
        item { SummaryCard(state.summary, partial = !state.exhausted) }

        state.message?.let { item { CommerceNotice(text = it) } }

        if (state.rows.isEmpty()) {
            item {
                UsEmptyState(
                    title = "Nothing settled yet",
                    detail = "Prepaid orders appear here once they are delivered. " +
                        "Cash-on-delivery money is settled separately.",
                    modifier = Modifier.padding(vertical = UsTheme.spacing.xxl),
                )
            }
        } else {
            items(state.rows, key = { it.orderItemId }) { row ->
                EarningRow(row = row, onClick = { onOpenOrder(row.orderId) })
            }
        }

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

/**
 * The sum of the rows on screen, labelled as exactly that.
 *
 * [partial] is set while more pages may exist, and the wording changes
 * with it: a figure headed "Net earned" over half a list is a figure the
 * seller will quote to their accountant.
 */
@Composable
private fun SummaryCard(summary: EarningsSummary, partial: Boolean) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .padding(UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = if (partial) "Net, across the ${summary.rows} rows loaded so far" else "Net earned",
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textSecondary,
        )
        Text(
            text = summary.net.formatWithSymbol(),
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = "Gross ${summary.gross.formatWithSymbol()} on ${summary.rows} delivered " +
                if (summary.rows == 1) "item" else "items",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}

@Composable
private fun EarningRow(row: SellerEarning, onClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .padding(UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                text = row.productTitle.ifBlank { "Item" },
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = row.net.formatWithSymbol(),
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(
            text = listOfNotNull(
                row.orderNumber.takeIf { it.isNotBlank() },
                "Qty ${row.quantity}",
                formatSellerTimestamp(row.deliveredAt)?.let { "delivered $it" },
            ).joinToString("  "),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
        Deduction("Gross", row.gross)
        Deduction("Commission", row.commission, negative = true)
        Deduction("Platform fee", row.platformFee, negative = true)
        Deduction("TDS", row.tds, negative = true)
        UsPillButton(
            text = "View order",
            onClick = onClick,
            filled = false,
        )
    }
}

@Composable
private fun Deduction(label: String, amount: Paise, negative: Boolean = false) {
    if (negative && amount.isZero) return
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )
        Text(
            text = if (negative) "-${amount.formatWithSymbol()}" else amount.formatWithSymbol(),
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}
