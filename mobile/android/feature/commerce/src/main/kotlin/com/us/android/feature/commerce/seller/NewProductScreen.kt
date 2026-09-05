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
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice

/**
 * Listing a product.
 *
 * One variant, deliberately. The server models sizes and colours, and offering
 * that on a first listing turns "sell a thing" into a data-modelling exercise.
 * A seller with variants can be given a richer form later; a seller with one
 * product currently has no form at all.
 *
 * ## Two things this screen is careful about
 *
 * **Money never becomes a float.** The price is typed as text and parsed
 * straight to integer paise, so `1299.99` becomes 129999 rather than passing
 * through 1299.9899999999998 on its way to the database. This is the one place
 * a human types the number every subsequent sale is charged at.
 *
 * **The GST rate is asked, never assumed.** A product without a tax class is
 * not untaxed — it is unsellable, because checkout resolves the rate under a
 * row lock and refuses when there is none. Until this screen existed there was
 * no endpoint listing the rates at all, so every product created through the
 * API had none and failed at the last step of a purchase with an error the
 * seller never saw.
 */
@Composable
fun NewProductScreen(
    onBack: () -> Unit,
    onCreated: (productId: String) -> Unit,
    viewModel: NewProductViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()

    UsScaffold(topBar = { UsTopBar(title = "New product", onBack = onBack) }) { padding ->
        when {
            form.loadingRates -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading GST rates",
            )

            // Without the rates there is no legal way to submit, so the screen
            // says so instead of showing a form whose button can never enable.
            form.taxClasses.isEmpty() -> UsErrorState(
                message = form.error
                    ?: "GST rates are unavailable, so a product cannot be listed right now.",
                modifier = Modifier.padding(padding),
                onRetry = viewModel::loadRates,
            )

            else -> NewProductForm(
                form = form,
                modifier = Modifier
                    .padding(padding)
                    .verticalScroll(rememberScrollState()),
                onChange = viewModel::update,
                onSubmit = { viewModel.submit(onCreated) },
            )
        }
    }
}

@Composable
private fun NewProductForm(
    form: NewProductForm,
    modifier: Modifier = Modifier,
    onChange: ((NewProductForm) -> NewProductForm) -> Unit,
    onSubmit: () -> Unit,
) {
    Column(
        modifier = modifier.padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsTextField(
            value = form.title,
            onValueChange = { v -> onChange { it.copy(title = v) } },
            label = "Product name",
            placeholder = "What buyers will see",
            enabled = !form.saving,
        )
        UsTextField(
            value = form.description,
            onValueChange = { v -> onChange { it.copy(description = v) } },
            label = "Description (optional)",
            enabled = !form.saving,
            singleLine = false,
        )

        PriceFields(form = form, onChange = onChange)

        UsTextField(
            value = form.openingStock,
            onValueChange = { v ->
                onChange { it.copy(openingStock = v.filter(Char::isDigit).take(MAX_STOCK_DIGITS)) }
            },
            label = "How many do you have?",
            placeholder = "0",
            keyboardType = KeyboardType.Number,
            enabled = !form.saving,
        )

        // Required, and no default. Picking one for the seller files the wrong
        // tax on every sale of anything that is not that rate, and it is
        // exactly the kind of default nobody ever revisits.
        UsChoiceRow(
            options = form.taxClasses.map { UsChoice(it.id, it.name) },
            selected = form.taxClassId,
            onSelect = { id -> onChange { it.copy(taxClassId = id) } },
            label = "GST rate",
            enabled = !form.saving,
            allowDeselect = false,
        )
        Text(
            text = "Your prices include GST. Buyers see one number.",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
        )

        CommerceNotice(
            text = "New products are reviewed before they go on sale.",
        )

        form.error?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
            )
        }

        UsButton(
            text = "List this product",
            onClick = onSubmit,
            enabled = form.isComplete && !form.saving,
            loading = form.saving,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun PriceFields(
    form: NewProductForm,
    onChange: ((NewProductForm) -> NewProductForm) -> Unit,
) {
    UsTextField(
        value = form.sellingPrice,
        onValueChange = { v -> onChange { it.copy(sellingPrice = priceInput(v)) } },
        label = "Price",
        placeholder = "What the buyer pays, GST included",
        keyboardType = KeyboardType.Decimal,
        // Said the moment it is typed, rather than after a failed submit: the
        // field looks complete, so a seller staring at a disabled button has
        // no way to tell which of five inputs is the problem.
        errorText = "Enter a price like 1299 or 1299.50"
            .takeIf { form.sellingPrice.isNotBlank() && form.sellingPaise == null },
        enabled = !form.saving,
    )
    UsTextField(
        value = form.mrp,
        onValueChange = { v -> onChange { it.copy(mrp = priceInput(v)) } },
        label = "Struck-through price (optional)",
        placeholder = "Leave empty if you are not running a discount",
        keyboardType = KeyboardType.Decimal,
        errorText = when {
            form.mrp.isNotBlank() && form.mrpPaise == null ->
                "Enter a price like 1499 or 1499.50"
            // A struck-through price below the selling price shows the buyer a
            // negative discount. Almost always the two typed the wrong way
            // round, and the seller wants to know before the listing is live.
            form.mrpBelowSelling ->
                "This is lower than your price. Did you swap the two?"
            else -> null
        },
        enabled = !form.saving,
    )
}

/**
 * Keeps a price field to digits and at most one separator.
 *
 * Filtering as it is typed rather than validating on submit means the field
 * can never hold something the parser will reject for a reason the seller
 * cannot see.
 */
private fun priceInput(raw: String): String {
    val kept = raw.filter { it.isDigit() || it == '.' }
    val firstDot = kept.indexOf('.')
    if (firstDot < 0) return kept.take(MAX_PRICE_DIGITS)
    val rupees = kept.substring(0, firstDot).take(MAX_PRICE_DIGITS)
    val paise = kept.substring(firstDot + 1).filter(Char::isDigit).take(2)
    return "$rupees.$paise"
}

private const val MAX_PRICE_DIGITS = 9
private const val MAX_STOCK_DIGITS = 6
