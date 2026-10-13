package com.us.android.feature.commerce.seller

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
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
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsSwitchRow
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.MSellerPageBar

/**
 * Changing what a listing costs.
 *
 * The current price is READ from the catalogue when this screen opens, not
 * carried through navigation. A price carried from a list is a price from
 * whenever that list loaded, and repricing against a stale figure is how a
 * seller undoes a change they made a minute ago on another device.
 *
 * Prices are sent as paise. ₹1,299.99 stays 129999 through an edit rather than
 * round-tripping through 1299.9899999999998, and the server moves the NUMERIC
 * mirror and the `_minor` column checkout reads together — moving only the
 * mirror is the original defect, where the seller saw the new price everywhere
 * and the buyer was charged the old one.
 */
@Composable
fun EditPriceScreen(
    title: String,
    onBack: () -> Unit,
    viewModel: EditPriceViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { MSellerPageBar(title = title.ifBlank { "Price" }, onBack = onBack) },
    ) { padding ->
        when (val s = state) {
            is EditPriceUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading the current price",
            )

            is EditPriceUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::refresh.takeIf { s.retryable },
            )

            is EditPriceUiState.Content -> EditPriceForm(
                state = s,
                modifier = Modifier
                    .padding(padding)
                    .verticalScroll(rememberScrollState()),
                onPrice = viewModel::setPrice,
                onMrp = viewModel::setMrp,
                onPaused = viewModel::setPaused,
                onSave = { viewModel.save(onBack) },
            )
        }
    }
}

@Composable
private fun EditPriceForm(
    state: EditPriceUiState.Content,
    modifier: Modifier = Modifier,
    onPrice: (String) -> Unit,
    onMrp: (String) -> Unit,
    onPaused: (Boolean) -> Unit,
    onSave: () -> Unit,
) {
    Column(
        modifier = modifier.padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        // What it costs today, from the server. Shown as the placeholder text
        // rather than pre-filled: a pre-filled field a seller does not touch
        // still gets sent, and re-asserting the same price is a write nobody
        // asked for.
        Text(
            text = "Selling for ${state.currentPrice.formatWithSymbol()}",
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )

        UsTextField(
            value = state.price,
            onValueChange = onPrice,
            label = "New price",
            placeholder = "Leave empty to keep ${state.currentPrice.formatWithSymbol()}",
            keyboardType = KeyboardType.Decimal,
            errorText = "Enter a price like 1299 or 1299.50".takeIf { state.priceMalformed },
            enabled = !state.saving,
        )
        UsTextField(
            value = state.mrp,
            onValueChange = onMrp,
            label = "New struck-through price",
            placeholder = "Leave empty to keep ${state.currentMrp.formatWithSymbol()}",
            keyboardType = KeyboardType.Decimal,
            errorText = when {
                state.mrpMalformed -> "Enter a price like 1499 or 1499.50"
                // Considers whichever of the two is being changed: editing
                // only the price can put it above an MRP that was fine before.
                state.mrpBelowPrice -> "This is below your selling price."
                else -> null
            },
            enabled = !state.saving,
        )

        // Applied immediately, not batched with the price. A seller pausing
        // something has usually just run out of it, and making them press Save
        // first is how stock gets sold that does not exist.
        //
        // The app's settings switch row, not a bare Material Switch: the whole
        // row is the target and the switch wears the app's colours, the same
        // as every other on/off in the product.
        UsSettingsSwitchRow(
            title = "On sale",
            description = if (state.paused) {
                "Paused — buyers cannot add this to a bag"
            } else {
                "Buyers can order this"
            },
            checked = !state.paused,
            onCheckedChange = { onSale -> onPaused(!onSale) },
            enabled = !state.saving,
        )

        if (state.saved) {
            CommerceNotice(text = "Saved.")
        }

        state.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }

        UsButton(
            text = "Save price",
            onClick = onSave,
            enabled = state.canSave,
            loading = state.saving,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}
