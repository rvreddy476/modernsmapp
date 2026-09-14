package com.us.android.feature.feast.restaurant

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.Paise
import com.us.android.core.food.model.toShortRupeeText
import com.us.android.core.food.network.FeastMenuItemDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.feature.feast.home.CartBar
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.FoodTypeMark
import com.us.android.feature.feast.ui.InfoNote
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.Pill
import com.us.android.feature.feast.ui.QuantityStepper
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone
import com.us.android.feature.feast.ui.listPadding

@Composable
@Suppress("LongMethod")
fun RestaurantScreen(
    onBack: () -> Unit,
    onOpenCart: () -> Unit,
    viewModel: RestaurantViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.refreshServiceability()
        onPauseOrDispose { }
    }

    state.sheet?.let { selection ->
        ItemSheet(
            selection = selection,
            onChange = viewModel::updateSheet,
            onConfirm = viewModel::confirmSheet,
            onDismiss = viewModel::dismissSheet,
        )
    }
    state.pendingReplace?.let {
        AlertDialog(
            onDismissRequest = viewModel::dismissReplaceCart,
            title = { Text("Start a new cart?") },
            text = { Text("Your cart has food from another restaurant. Adding this clears it.") },
            confirmButton = {
                TextButton(onClick = viewModel::confirmReplaceCart) { Text("Start new cart", color = UsTheme.extended.accentSolid) }
            },
            dismissButton = {
                TextButton(onClick = viewModel::dismissReplaceCart) { Text("Keep my cart", color = UsTheme.extended.textMuted) }
            },
            containerColor = UsTheme.extended.bgRaised,
            titleContentColor = UsTheme.extended.textPrimary,
            textContentColor = UsTheme.extended.textSecondary,
        )
    }

    FeastScreen(
        title = state.restaurant?.name.orEmpty(),
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.cartItemCount > 0 && state.cart?.restaurantId == state.restaurant?.id) {
                CartBar(itemCount = state.cartItemCount, restaurant = "", total = null, onClick = onOpenCart)
            }
        },
    ) { padding ->
        val restaurant = state.restaurant
        when {
            state.loading -> LoadingPane()
            restaurant == null -> MessagePane(
                title = "Restaurant unavailable",
                body = state.loadError.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                item { Header(restaurant, state.serviceability) }
                if (state.categories.isEmpty()) {
                    item {
                        MessagePane(
                            title = "No menu yet",
                            body = state.loadError ?: "This restaurant hasn't published its menu.",
                            icon = UsIcons.Utensils,
                            modifier = Modifier.padding(top = 24.dp),
                        )
                    }
                }
                state.categories.forEach { category ->
                    item(key = "cat-${category.id}") { SectionLabel(category.name) }
                    items(category.items, key = { it.id }) { item ->
                        MenuItemRow(
                            item = item,
                            adding = state.addingItemId == item.id,
                            orderable = state.serviceability is Serviceability.Open,
                            onAdd = { viewModel.onAdd(item) },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun Header(restaurant: FeastRestaurantDto, serviceability: Serviceability) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Text(
            text = restaurant.name,
            style = MaterialTheme.typography.headlineSmall.copy(fontFamily = MomentumWordmarkFontFamily),
            color = UsTheme.extended.textPrimary,
        )
        val sub = listOf(restaurant.cuisines.joinToString(" · "), restaurant.addressLine.orEmpty().ifBlank { restaurant.city })
            .filter { it.isNotBlank() }
            .joinToString("  ·  ")
        if (sub.isNotBlank()) Text(sub, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), verticalAlignment = Alignment.CenterVertically) {
            if (serviceability is Serviceability.Open) Pill("Open now", Tone.Positive) else Pill("Can't order right now", Tone.Danger)
            if (restaurant.avgPreparationMinutes > 0) Pill("${restaurant.avgPreparationMinutes} min prep", Tone.Neutral)
            if (restaurant.minOrderAmount > Paise.ZERO) Pill("Min ${restaurant.minOrderAmount.toShortRupeeText()}", Tone.Neutral)
        }
        if (serviceability is Serviceability.Blocked) {
            FeastCard {
                InfoNote(text = serviceability.message, tone = Tone.Danger)
            }
        }
    }
}

@Composable
private fun MenuItemRow(item: FeastMenuItemDto, adding: Boolean, orderable: Boolean, onAdd: () -> Unit) {
    FeastCard {
        Row(verticalAlignment = Alignment.Top) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    FoodTypeMark(item.foodType)
                    if (item.isRecommended) {
                        Spacer(Modifier.width(UsTheme.spacing.m))
                        Pill("Bestseller", Tone.Accent)
                    }
                }
                Text(item.name, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                PriceLine(item)
                item.description?.takeIf { it.isNotBlank() }?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
                if (item.variants.isNotEmpty() || item.addonGroups.isNotEmpty()) {
                    Text("Customisable", style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textDim)
                }
            }
            Spacer(Modifier.width(UsTheme.spacing.l))
            when {
                !item.isAvailable -> Pill("Unavailable", Tone.Neutral)
                else -> UsPillButton(
                    text = "Add",
                    onClick = onAdd,
                    filled = orderable,
                    busy = adding,
                )
            }
        }
    }
}

/** The menu price: the discounted price when the restaurant set one, with the base struck through. */
@Composable
private fun PriceLine(item: FeastMenuItemDto) {
    val discount = item.discountPricePaise?.takeIf { it > Paise.ZERO && it < item.basePricePaise }
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = (discount ?: item.basePricePaise).toShortRupeeText(),
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
            fontWeight = FontWeight.SemiBold,
        )
        if (discount != null) {
            Text(
                text = item.basePricePaise.toShortRupeeText(),
                style = MaterialTheme.typography.bodySmall.copy(textDecoration = TextDecoration.LineThrough),
                color = UsTheme.extended.textDim,
            )
        }
    }
}

/**
 * Sizes (one) and add-ons (per group, within min/max). Each option shows its
 * own server price; the sheet adds nothing up — the cart shows the server's
 * figure once the item is in it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
@Suppress("LongMethod")
private fun ItemSheet(
    selection: ItemSelection,
    onChange: (ItemSelection) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val item = selection.item
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgRaised,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding()
                .padding(bottom = UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                FoodTypeMark(item.foodType)
                Spacer(Modifier.width(UsTheme.spacing.m))
                Text(item.name, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
            }
            if (item.variants.isNotEmpty()) {
                SectionLabel("Choose a size")
                item.variants.sortedBy { it.sortOrder }.forEach { variant ->
                    OptionRow(
                        label = variant.name,
                        price = variant.pricePaise.toShortRupeeText(),
                        enabled = variant.isAvailable,
                        onClick = { onChange(selection.copy(variantId = variant.id)) },
                    ) {
                        RadioButton(
                            selected = selection.variantId == variant.id,
                            onClick = { onChange(selection.copy(variantId = variant.id)) },
                            enabled = variant.isAvailable,
                            colors = RadioButtonDefaults.colors(selectedColor = UsTheme.extended.accentSolid),
                        )
                    }
                }
            }
            item.addonGroups.sortedBy { it.sortOrder }.forEach { group ->
                val rule = when {
                    group.isRequired || group.minSelect > 0 -> "Required · choose ${maxOf(group.minSelect, 1)}" +
                        if (group.maxSelect > maxOf(group.minSelect, 1)) " to ${group.maxSelect}" else ""
                    group.maxSelect > 0 -> "Optional · up to ${group.maxSelect}"
                    else -> "Optional"
                }
                SectionLabel(group.name) { Text(rule, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textDim) }
                group.addons.sortedBy { it.sortOrder }.forEach { addon ->
                    val checked = addon.id in selection.addonIds
                    val toggle = {
                        onChange(selection.copy(addonIds = if (checked) selection.addonIds - addon.id else selection.addonIds + addon.id))
                    }
                    OptionRow(
                        label = addon.name,
                        price = "+ ${addon.pricePaise.toShortRupeeText()}",
                        enabled = addon.isAvailable,
                        onClick = toggle,
                    ) {
                        Checkbox(
                            checked = checked,
                            onCheckedChange = { toggle() },
                            enabled = addon.isAvailable,
                            colors = CheckboxDefaults.colors(checkedColor = UsTheme.extended.accentSolid),
                        )
                    }
                }
            }
            Row(
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                QuantityStepper(
                    quantity = selection.quantity,
                    onDecrease = { onChange(selection.copy(quantity = (selection.quantity - 1).coerceAtLeast(1))) },
                    onIncrease = {
                        onChange(selection.copy(quantity = (selection.quantity + 1).coerceAtMost(ItemSelectionRules.MAX_QUANTITY)))
                    },
                )
                Spacer(Modifier.width(UsTheme.spacing.xxl))
                UsButton(
                    text = "Add to cart",
                    onClick = onConfirm,
                    enabled = ItemSelectionRules.problem(selection) == null,
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

@Composable
private fun OptionRow(
    label: String,
    price: String,
    enabled: Boolean,
    onClick: () -> Unit,
    control: @Composable () -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth().clickable(enabled = enabled, onClick = onClick),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        control()
        Text(
            text = if (enabled) label else "$label (unavailable)",
            style = MaterialTheme.typography.bodyMedium,
            color = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textDim,
            modifier = Modifier.weight(1f),
        )
        Text(price, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
    }
}
