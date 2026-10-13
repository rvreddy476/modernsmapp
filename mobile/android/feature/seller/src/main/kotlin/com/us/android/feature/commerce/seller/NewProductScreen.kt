package com.us.android.feature.commerce.seller

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.CategoryChoice
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceSheet
import com.us.android.feature.commerce.ui.MSellerPageBar
import com.us.android.feature.commerce.ui.pressScale

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
    onCreated: (CreatedProduct) -> Unit,
    viewModel: NewProductViewModel = hiltViewModel(),
    images: ProductImagesViewModel = hiltViewModel(),
) {
    val form by viewModel.form.collectAsStateWithLifecycle()
    val gallery by images.state.collectAsStateWithLifecycle()
    var choosingCategory by rememberSaveable { mutableStateOf(false) }

    // The gallery can only be attached once the listing has an id, so the
    // create happens first and the images follow it. A seller who added no
    // photos is not held up by an attach with nothing to send.
    val submit: () -> Unit = {
        viewModel.submit { created ->
            if (readyMediaIds(gallery.images).isEmpty()) {
                onCreated(created)
            } else {
                images.attach(created.productId) { onCreated(created) }
            }
        }
    }

    if (choosingCategory) {
        CategoryPickerSheet(
            choices = form.categories,
            selected = form.categoryId,
            onChoose = { id ->
                viewModel.update { it.copy(categoryId = id) }
                choosingCategory = false
            },
            onDismiss = { choosingCategory = false },
        )
    }

    UsScaffold(topBar = { MSellerPageBar(title = "New product", onBack = onBack) }) { padding ->
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
                gallery = gallery,
                images = images,
                modifier = Modifier
                    .padding(padding)
                    .verticalScroll(rememberScrollState()),
                onChange = viewModel::update,
                onChooseCategory = { choosingCategory = true },
                onSubmit = submit,
            )
        }
    }
}

@Composable
private fun NewProductForm(
    form: NewProductForm,
    gallery: ProductImagesState,
    images: ProductImagesViewModel,
    modifier: Modifier = Modifier,
    onChange: ((NewProductForm) -> NewProductForm) -> Unit,
    onChooseCategory: () -> Unit,
    onSubmit: () -> Unit,
) {
    Column(
        modifier = modifier.padding(vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        // Photos first: it is the part of a listing a seller most wants to
        // get right, and a product with no picture is one buyers scroll past.
        ProductImagesSection(
            state = gallery,
            onPicked = images::onPicked,
            onRemove = images::remove,
            onMove = images::move,
            onMakeCover = images::makeCover,
        )

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

        // Required (2026-09-30): a product with no category appears in no
        // category, so a buyer browsing never reaches it. Chosen from the
        // tree's listable leaves, never preselected.
        CategoryRow(
            form = form,
            enabled = !form.saving,
            onClick = onChooseCategory,
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
            // Held while a photo is still uploading: listing now would create
            // the product without exactly the images the seller is watching
            // finish, and there would be nothing to tell them so.
            enabled = form.isComplete && !form.saving && !gallery.uploading && !gallery.attaching,
            loading = form.saving || gallery.attaching,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/** The category row: what is chosen, or the invitation to choose. */
@Composable
private fun CategoryRow(
    form: NewProductForm,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Text(
            text = "Category",
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textSecondary,
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.medium))
                .background(UsTheme.extended.bgCard)
                .pressScale(onClick = onClick, enabled = enabled)
                .padding(UsTheme.spacing.m)
                .testTag("mseller_category"),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            val label = form.categoryLabel
            Text(
                text = when {
                    label != null -> label
                    form.loadingCategories -> "Loading categories"
                    form.categories.isEmpty() -> "No categories to choose from"
                    else -> CHOOSE_A_CATEGORY
                },
                style = MaterialTheme.typography.bodyMedium,
                color = if (label != null) UsTheme.extended.textPrimary else UsTheme.extended.textSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Icon(
                imageVector = UsIcons.ChevronRight,
                contentDescription = null,
                tint = UsTheme.extended.textSecondary,
                modifier = Modifier.size(CHEVRON),
            )
        }
    }
}

/**
 * The listable categories, by path, one per row. A sheet rather than a
 * choice row: a taxonomy has dozens of leaves, and a row of chips that
 * wraps ten lines is not a control.
 */
@Composable
private fun CategoryPickerSheet(
    choices: List<CategoryChoice>,
    selected: String?,
    onChoose: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    CommerceSheet(title = "Category", onDismiss = onDismiss) {
        CategoryPickerList(choices = choices, selected = selected, onChoose = onChoose)
    }
}

/** The picker's body, apart from the sheet so it can be previewed. */
@Composable
private fun CategoryPickerList(
    choices: List<CategoryChoice>,
    selected: String?,
    onChoose: (String) -> Unit,
) {
    if (choices.isEmpty()) {
        Text(
            text = "No categories to choose from right now.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
    }
    LazyColumn(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(max = PICKER_MAX_HEIGHT)
            .testTag("mseller_category_picker"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        items(choices, key = { it.id }) { choice ->
            val chosen = choice.id == selected
            Text(
                text = choice.label,
                style = MaterialTheme.typography.bodyMedium,
                color = if (chosen) UsTheme.extended.accentSolid else UsTheme.extended.textPrimary,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .pressScale(onClick = { onChoose(choice.id) })
                    .padding(UsTheme.spacing.m)
                    .testTag("mseller_category:${choice.id}"),
            )
        }
    }
}

private val CHEVRON = 18.dp
private val PICKER_MAX_HEIGHT = 420.dp

private val previewCategories = listOf(
    CategoryChoice(id = "c-1", label = "Electronics"),
    CategoryChoice(id = "c-2", label = "Books & Stationery › Textbooks"),
    CategoryChoice(id = "c-3", label = "Home & Kitchen"),
)

@Preview(showBackground = true)
@Composable
private fun CategoryRowPreview() {
    UsTheme {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            CategoryRow(
                form = NewProductForm(categories = previewCategories, loadingCategories = false),
                enabled = true,
                onClick = {},
            )
            CategoryRow(
                form = NewProductForm(
                    categories = previewCategories,
                    categoryId = "c-2",
                    loadingCategories = false,
                ),
                enabled = true,
                onClick = {},
            )
        }
    }
}

@Preview(showBackground = true)
@Composable
private fun CategoryPickerListPreview() {
    UsTheme {
        CategoryPickerList(choices = previewCategories, selected = "c-2", onChoose = {})
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
