package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
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
import com.us.android.core.commerce.model.ReturnStatus
import com.us.android.core.commerce.model.SellerReturn
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceSheet
import com.us.android.feature.commerce.ui.MSellerPageBar

fun ReturnStatus.label(): String = when (this) {
    ReturnStatus.REQUESTED -> "Waiting on you"
    ReturnStatus.APPROVED -> "Approved, pickup booked"
    ReturnStatus.REJECTED -> "Rejected"
    ReturnStatus.REFUNDED -> "Refunded"
    ReturnStatus.CLOSED -> "Closed"
    ReturnStatus.UNKNOWN -> "Updating"
}

/** The buyer's reason code, in words. Unknown codes are shown as sent rather than dropped. */
fun returnReasonLabel(code: String): String = when (code.lowercase()) {
    "damaged" -> "Arrived damaged"
    "defective" -> "Not working"
    "wrong_item" -> "Wrong item sent"
    "not_as_described" -> "Not as described"
    "size_fit" -> "Size or fit"
    "changed_mind" -> "Changed their mind"
    "late" -> "Arrived too late"
    else -> code.replace('_', ' ').ifBlank { "No reason given" }
}

@Composable
fun SellerReturnsScreen(
    onBack: () -> Unit,
    onOpenOrder: (orderId: String) -> Unit,
    viewModel: SellerReturnsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { MSellerPageBar(title = "Returns", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            SellerReturnsUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading returns",
            )

            is SellerReturnsUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is SellerReturnsUiState.Content -> {
                s.rejecting?.let { RejectSheet(state = s, viewModel = viewModel) }
                ReturnsContent(
                    state = s,
                    padding = padding,
                    onSelect = viewModel::select,
                    onApprove = viewModel::approve,
                    onReject = viewModel::openReject,
                    onOpenOrder = onOpenOrder,
                )
            }
        }
    }
}

@Composable
private fun ReturnsContent(
    state: SellerReturnsUiState.Content,
    padding: PaddingValues,
    onSelect: (ReturnFilter) -> Unit,
    onApprove: (String) -> Unit,
    onReject: (SellerReturn) -> Unit,
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
        item {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                for (filter in ReturnFilter.entries) {
                    UsPillButton(
                        text = filter.label,
                        onClick = { onSelect(filter) },
                        filled = filter == state.filter,
                        enabled = state.busyId == null,
                    )
                }
            }
        }

        if (state.rejecting == null) {
            state.error?.let { item { CommerceNotice(text = it) } }
        }

        if (state.returns.isEmpty()) {
            item {
                UsEmptyState(
                    title = "No returns",
                    detail = when (state.filter) {
                        ReturnFilter.OPEN -> "Nothing is waiting on you."
                        else -> "Nothing here yet."
                    },
                    modifier = Modifier.padding(vertical = UsTheme.spacing.xxl),
                )
            }
        } else {
            items(state.returns, key = { it.id }) { item ->
                ReturnCard(
                    item = item,
                    busy = state.busyId == item.id,
                    anyBusy = state.busyId != null,
                    onApprove = { onApprove(item.id) },
                    onReject = { onReject(item) },
                    onOpenOrder = { onOpenOrder(item.orderId) },
                )
            }
        }
    }
}

@Composable
private fun ReturnCard(
    item: SellerReturn,
    busy: Boolean,
    anyBusy: Boolean,
    onApprove: () -> Unit,
    onReject: () -> Unit,
    onOpenOrder: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard)
            .padding(UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CommerceImage(
                url = item.imageUrl,
                contentDescription = item.itemTitle,
                modifier = Modifier.size(56.dp),
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = item.itemTitle ?: "Returned item",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = listOfNotNull(
                        item.orderNumber,
                        item.quantity?.let { "Qty $it" },
                        item.itemSku?.takeIf { it.isNotBlank() }?.let { "SKU $it" },
                    ).joinToString("  "),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
            item.lineTotal?.let {
                Text(
                    text = it.formatWithSymbol(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                )
            }
        }

        Text(
            text = returnReasonLabel(item.reasonCode),
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
        )
        item.reasonDescription?.takeIf { it.isNotBlank() }?.let { detail ->
            Text(
                text = "“$detail”",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        Text(
            text = listOfNotNull(
                item.status.label(),
                formatSellerTimestamp(item.requestedAt)?.let { "asked $it" },
            ).joinToString(", "),
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )
        item.refundAmount?.let { refund ->
            Text(
                text = "Refund on approval: ${refund.formatWithSymbol()}",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        item.rejectionReason?.takeIf { it.isNotBlank() }?.let { reason ->
            Text(
                text = "You said: $reason",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textSecondary,
            )
        }

        if (item.status.awaitingDecision) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                UsPillButton(
                    text = "Approve",
                    onClick = onApprove,
                    enabled = !anyBusy,
                    busy = busy,
                )
                UsPillButton(
                    text = "Reject",
                    onClick = onReject,
                    filled = false,
                    enabled = !anyBusy,
                )
            }
        }
        UsPillButton(
            text = "View order",
            onClick = onOpenOrder,
            filled = false,
        )
    }
}

@Composable
private fun RejectSheet(
    state: SellerReturnsUiState.Content,
    viewModel: SellerReturnsViewModel,
) {
    val busy = state.busyId != null
    CommerceSheet(title = "Reject this return?", onDismiss = viewModel::dismissReject) {
        Text(
            text = "The buyer reads this reason. Say what you checked and why it does not qualify.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        UsTextField(
            value = state.rejectReason,
            onValueChange = viewModel::updateRejectReason,
            label = "Reason",
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
            text = "Reject return",
            onClick = viewModel::reject,
            enabled = state.canSubmitReject,
            loading = busy,
            modifier = Modifier.fillMaxWidth(),
        )
        UsSecondaryButton(
            text = "Go back",
            onClick = viewModel::dismissReject,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}
