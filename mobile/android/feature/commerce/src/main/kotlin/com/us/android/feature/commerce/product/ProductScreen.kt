package com.us.android.feature.commerce.product

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.Variant
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceImage
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.PriceRow
import com.us.android.feature.commerce.ui.pressScale

/**
 * Product detail.
 *
 * The buy button is disabled until a variant is chosen and the server says
 * that variant is in stock. Both halves matter: an enabled button that then
 * fails is worse than a disabled one that explains itself.
 */
@Composable
fun ProductScreen(
    onBack: () -> Unit,
    onOpenCart: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: ProductViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = "Product", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val s = state) {
            ProductUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading product",
            )

            is ProductUiState.Failed -> UsErrorState(
                message = s.message,
                modifier = Modifier.padding(padding),
                onRetry = viewModel::retry.takeIf { s.retryable },
            )

            is ProductUiState.Content -> ProductContent(
                state = s,
                modifier = Modifier.padding(padding),
                onSelectVariant = viewModel::selectVariant,
                onQuantityChange = viewModel::setQuantity,
                onAddToCart = viewModel::addToCart,
                onOpenCart = onOpenCart,
            )
        }
    }
}

@Composable
@Suppress("LongMethod")
private fun ProductContent(
    state: ProductUiState.Content,
    modifier: Modifier,
    onSelectVariant: (Variant) -> Unit,
    onQuantityChange: (Int) -> Unit,
    onAddToCart: () -> Unit,
    onOpenCart: () -> Unit,
) {
    val product = state.product
    Column(
        modifier = modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        // The full-size variant, not the thumbnail: this is a full-width hero
        // on a detail page, and the grid's thumbnail would be visibly soft at
        // this size. The catalogue makes the opposite choice for the opposite
        // reason — twenty full-size images to draw a grid is forty megabytes.
        CommerceImage(
            url = product.imageUrl,
            contentDescription = product.title,
            modifier = Modifier.fillMaxWidth(),
        )

        product.brandName?.takeIf { it.isNotBlank() }?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textSecondary,
            )
        }
        Text(
            text = product.title,
            style = MaterialTheme.typography.headlineSmall,
            color = UsTheme.extended.textPrimary,
        )

        state.selectedVariant?.let { variant ->
            PriceRow(price = variant.sellingPrice, mrp = variant.mrp)
        }

        if (product.reviewCount > 0) {
            Text(
                text = "${product.avgRating} ★ · ${product.reviewCount} reviews",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }

        if (product.variants.size > 1) {
            VariantPicker(
                variants = product.variants,
                selected = state.selectedVariant,
                onSelect = onSelectVariant,
            )
        }

        state.selectedVariant?.let { variant ->
            StockLine(variant)
            if (variant.inStock) {
                QuantityStepper(
                    quantity = state.quantity,
                    max = state.maxQuantity,
                    onChange = onQuantityChange,
                )
            }
        }

        product.description?.takeIf { it.isNotBlank() }?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
        }

        product.sellerName?.takeIf { it.isNotBlank() }?.let {
            Text(
                text = "Sold by $it",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }

        state.message?.let { CommerceNotice(text = it) }

        if (state.addedToCart) {
            UsSecondaryButton(
                text = "Go to cart",
                onClick = onOpenCart,
                modifier = Modifier.fillMaxWidth(),
            )
        }

        UsButton(
            text = when {
                state.selectedVariant == null -> "Select an option"
                state.selectedVariant?.inStock == false -> "Out of stock"
                else -> "Add to cart"
            },
            onClick = onAddToCart,
            enabled = state.canAddToCart,
            loading = state.adding,
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = UsTheme.spacing.xxl),
        )
    }
}

@Composable
private fun VariantPicker(
    variants: List<Variant>,
    selected: Variant?,
    onSelect: (Variant) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Text(
            text = "Options",
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
        )
        variants.forEach { variant ->
            val isSelected = variant.id == selected?.id
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    // Selected is WHITE. The accent is the app's primary
                    // action, not its selection mark, and an ember ring here
                    // competes with the Add to cart button below it.
                    .border(
                        width = if (isSelected) SELECTED_BORDER else UNSELECTED_BORDER,
                        color = if (isSelected) Color.White else UsTheme.extended.borderSubtle,
                        shape = RoundedCornerShape(UsTheme.radii.medium),
                    )
                    .background(UsTheme.extended.bgCard)
                    // A sold-out variant stays SELECTABLE so the buyer can
                    // see its price and confirm it is the one that is gone.
                    // Only the add button is disabled.
                    .pressScale(onClick = { onSelect(variant) }, role = Role.RadioButton)
                    .padding(UsTheme.spacing.l),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = variant.options.joinToString(" · ") { "${it.name}: ${it.value}" }
                        .ifBlank { variant.sku },
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = if (variant.inStock) {
                        variant.sellingPrice.formatWithSymbol()
                    } else {
                        "Out of stock"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }
    }
}

@Composable
private fun StockLine(variant: Variant) {
    val text = when {
        !variant.inStock -> "Out of stock"
        variant.availableQty <= LOW_STOCK_THRESHOLD -> "Only ${variant.availableQty} left"
        else -> "In stock"
    }
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textSecondary,
    )
}

@Composable
private fun QuantityStepper(quantity: Int, max: Int, onChange: (Int) -> Unit) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "Quantity",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
        StepperButton(
            label = "−",
            description = "One fewer",
            enabled = quantity > 1,
        ) { onChange(quantity - 1) }
        Text(
            text = quantity.toString(),
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )
        StepperButton(
            label = "+",
            description = "One more",
            enabled = quantity < max,
        ) { onChange(quantity + 1) }
    }
}

/**
 * The − / + chip.
 *
 * [description] exists because the labels are typographic signs: a screen
 * reader announces "+" as "plus" at best and says nothing at worst, so the
 * button states what it does instead.
 */
@Composable
private fun StepperButton(
    label: String,
    description: String,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    Text(
        text = label,
        style = MaterialTheme.typography.titleMedium,
        color = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textDim,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.bgCard)
            .pressScale(onClick = onClick, enabled = enabled)
            .semantics { contentDescription = description }
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
    )
}

private const val LOW_STOCK_THRESHOLD = 5
private val SELECTED_BORDER = 2.dp
private val UNSELECTED_BORDER = 1.dp

@Preview(showBackground = true)
@Composable
private fun QuantityStepperPreview() {
    UsTheme { QuantityStepper(quantity = 2, max = 5, onChange = {}) }
}
