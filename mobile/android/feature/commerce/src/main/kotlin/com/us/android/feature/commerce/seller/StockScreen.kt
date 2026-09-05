package com.us.android.feature.commerce.seller

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.StockReason
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar

/**
 * Stock for one variant.
 *
 * The screen asks how many units were ADDED or REMOVED â never what the new
 * total is. That is not a UI preference: a new-total field is a lost-update
 * generator. The screen renders 42, two units sell while the seller is typing,
 * they submit 52 meaning "I added ten", and the two sold units are silently
 * restored to the shelf. The seller knows exactly one true number, and it is
 * the delta.
 */
@Composable
fun StockScreen(
    title: String,
    onBack: () -> Unit,
    onEditPrice: () -> Unit,
    viewModel: StockViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(topBar = { MSellerPageBar(title = title.ifBlank { "Stock" }, onBack = onBack) }) { padding ->
        when (val s = state) {
            is StockUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading stock",
            )

            is StockUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is StockUiState.Content -> StockForm(
                state = s,
                onEditPrice = onEditPrice,
                modifier = Modifier
                    .padding(padding)
                    .verticalScroll(rememberScrollState()),
                onAmount = viewModel::setAmount,
                onRemoving = viewModel::setRemoving,
                onReason = viewModel::setReason,
                onNotes = viewModel::setNotes,
                onSubmit = viewModel::submit,
            )
        }
    }
}

@Composable
private fun StockForm(
    state: StockUiState.Content,
    modifier: Modifier = Modifier,
    onEditPrice: () -> Unit,
    onAmount: (String) -> Unit,
    onRemoving: (Boolean) -> Unit,
    onReason: (StockReason) -> Unit,
    onNotes: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Column(
        modifier = modifier.padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        StockHeader(state = state, onEditPrice = onEditPrice)

        // allowDeselect = false: there is no third direction. A cleared
        // selection here would leave the form in a state whose submit button
        // means neither "add" nor "remove".
        UsChoiceRow(
            options = listOf(
                UsChoice(false, "I received new stock"),
                UsChoice(true, "I am removing stock"),
            ),
            selected = state.removing,
            onSelect = { onRemoving(it ?: false) },
            label = "What changed?",
            enabled = !state.saving,
            allowDeselect = false,
        )

        UsTextField(
            value = state.amount,
            onValueChange = onAmount,
            label = if (state.removing) "Units removed" else "Units added",
            placeholder = "0",
            keyboardType = KeyboardType.Number,
            enabled = !state.saving,
        )

        // Every movement carries a stated cause. A stock change with no reason
        // is unauditable, which is why there is no "other" here, why the
        // selection cannot be cleared, and why the server keeps its own
        // allow-list rather than trusting this one.
        UsChoiceRow(
            options = reasonsFor(removing = state.removing)
                .map { UsChoice(it, it.label) },
            selected = state.reason,
            onSelect = { it?.let(onReason) },
            label = "Why?",
            enabled = !state.saving,
            allowDeselect = false,
        )

        UsTextField(
            value = state.notes,
            onValueChange = onNotes,
            label = "Note (optional)",
            enabled = !state.saving,
            singleLine = false,
        )

        // Said before the round trip, not after a 409. The server holds the
        // real floor under a row lock and will refuse regardless â this is so
        // the seller understands why the button is doing nothing.
        if (state.wouldBreachReserved) {
            CommerceNotice(
                text = "You cannot remove that many. ${state.level.reserved} " +
                    "unit(s) are reserved for orders being placed right now.",
            )
        }

        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }

        UsButton(
            text = if (state.removing) "Remove stock" else "Add stock",
            onClick = onSubmit,
            enabled = state.canSubmit,
            loading = state.saving,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/**
 * The reasons that make sense in each direction.
 *
 * Offering "Damaged" as a reason for receiving stock, or "New stock arrived"
 * as a reason for removing it, invites a mis-tap that lands in the audit trail
 * and stays there.
 */
private fun reasonsFor(removing: Boolean): List<StockReason> = if (removing) {
    listOf(StockReason.DAMAGE, StockReason.THEFT, StockReason.RECOUNT, StockReason.CORRECTION)
} else {
    listOf(StockReason.PURCHASE, StockReason.RECOUNT, StockReason.CORRECTION)
}

@Composable
private fun StockFigure(label: String, value: Int) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Text(
            text = value.toString(),
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}

/**
 * What the variant looks like right now, and the way to its price.
 *
 * Three numbers, not one: a seller looking at "42" needs to know how many are
 * already promised to orders being placed this second before deciding what
 * they can physically ship today.
 */
@Composable
private fun StockHeader(state: StockUiState.Content, onEditPrice: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        StockFigure("In stock", state.level.total)
        StockFigure("Reserved", state.level.reserved)
        StockFigure("Available", state.level.available)
    }

    // The other thing a seller opens a variant to change. Kept on this screen
    // rather than the hub because stock and price are the two numbers that go
    // stale together — a sell-out is usually followed by a restock and a look
    // at the price.
    UsSecondaryButton(
        text = "Change price",
        onClick = onEditPrice,
        modifier = Modifier.fillMaxWidth(),
    )

    state.lastChange?.let { change ->
        CommerceNotice(
            text = if (change > 0) {
                "Added $change. Stock is now ${state.level.total}."
            } else {
                "Removed ${-change}. Stock is now ${state.level.total}."
            },
        )
    }
}
