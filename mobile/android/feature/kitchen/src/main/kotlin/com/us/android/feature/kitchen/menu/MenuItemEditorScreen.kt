package com.us.android.feature.kitchen.menu

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.kitchenSwitchColors

private val FOOD_TYPES = listOf(
    FOOD_VEG to "Veg",
    FOOD_VEGAN to "Vegan",
    FOOD_JAIN to "Jain",
    FOOD_EGG to "Egg",
    FOOD_NON_VEG to "Non-veg",
)

/** All five food-service food types, wrapping onto a second line on narrow screens. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun FoodTypePicker(selected: String, onSelect: (String) -> Unit) {
    Column {
        Text("Type", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        FlowRow(
            modifier = Modifier.padding(top = UsTheme.spacing.s),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            FOOD_TYPES.forEach { (value, label) ->
                FilterChip(
                    selected = selected == value,
                    onClick = { onSelect(value) },
                    label = { Text(label) },
                    leadingIcon = { FoodTypeMark(value) },
                    colors = FilterChipDefaults.filterChipColors(
                        containerColor = UsTheme.extended.bgCardSolid,
                        labelColor = UsTheme.extended.textSecondary,
                        selectedContainerColor = UsTheme.extended.unreadRow,
                        selectedLabelColor = UsTheme.extended.textPrimary,
                    ),
                )
            }
        }
    }
}

@Composable
fun MenuItemEditorScreen(onBack: () -> Unit, viewModel: MenuItemEditorViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.finished) {
        if (state.finished) onBack()
    }
    if (state.confirmingDelete) {
        AlertDialog(
            onDismissRequest = viewModel::cancelDelete,
            title = { Text("Delete this dish?") },
            text = { Text("It disappears from your menu. Past orders keep their record.") },
            confirmButton = {
                TextButton(onClick = viewModel::delete) { Text("Delete", color = UsTheme.extended.statusDanger) }
            },
            dismissButton = {
                TextButton(onClick = viewModel::cancelDelete) { Text("Keep", color = UsTheme.extended.textMuted) }
            },
            containerColor = UsTheme.extended.bgRaised,
            titleContentColor = UsTheme.extended.textPrimary,
            textContentColor = UsTheme.extended.textSecondary,
        )
    }
    KitchenScreen(
        title = if (state.isNew) "New dish" else "Edit dish",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        actions = {
            if (!state.isNew) {
                IconButton(onClick = viewModel::askDelete, enabled = !state.deleting) {
                    Icon(UsIcons.Trash, contentDescription = "Delete dish", tint = UsTheme.extended.textMuted)
                }
            }
        },
    ) { padding ->
        if (state.loading) {
            LoadingPane(Modifier.padding(padding))
            return@KitchenScreen
        }
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 96.dp),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsTextField(
                value = state.name,
                onValueChange = viewModel::onName,
                label = "Dish name",
                errorText = state.errors[MenuItemRules.FIELD_NAME],
            )
            UsTextField(
                value = state.description,
                onValueChange = viewModel::onDescription,
                label = "Description (optional)",
                singleLine = false,
            )
            FoodTypePicker(selected = state.foodType, onSelect = viewModel::onFoodType)
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                UsTextField(
                    value = state.price,
                    onValueChange = viewModel::onPrice,
                    label = "Price (₹)",
                    placeholder = "249",
                    errorText = state.errors[MenuItemRules.FIELD_PRICE],
                    keyboardType = KeyboardType.Decimal,
                    modifier = Modifier.weight(1f),
                )
                UsTextField(
                    value = state.offer,
                    onValueChange = viewModel::onOffer,
                    label = "Offer price (₹)",
                    placeholder = "Optional",
                    errorText = state.errors[MenuItemRules.FIELD_OFFER],
                    keyboardType = KeyboardType.Decimal,
                    modifier = Modifier.weight(1f),
                )
            }
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                UsTextField(
                    value = state.prepMinutes,
                    onValueChange = viewModel::onPrepMinutes,
                    label = "Prep time (min)",
                    errorText = state.errors[MenuItemRules.FIELD_PREP],
                    keyboardType = KeyboardType.Number,
                    modifier = Modifier.weight(1f),
                )
                UsTextField(
                    value = state.taxPercent,
                    onValueChange = viewModel::onTaxPercent,
                    label = "GST rate (%)",
                    errorText = state.errors[MenuItemRules.FIELD_TAX],
                    keyboardType = KeyboardType.Decimal,
                    modifier = Modifier.weight(1f),
                )
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                CardHeading(
                    title = "Recommended",
                    body = "Shown near the top of your menu",
                    modifier = Modifier.weight(1f),
                )
                Switch(checked = state.recommended, onCheckedChange = viewModel::onRecommended, colors = kitchenSwitchColors())
            }

            SectionHeader("Photo")
            KitchenCard {
                CardHeading(
                    title = "Dish photo",
                    body = "Adding photos to dishes isn't available yet. The server takes an image address for dishes, not an uploaded photo.",
                )
            }

            SectionHeader("Sizes and add-ons")
            KitchenCard {
                CardHeading(title = "Variants and add-on groups", body = state.extrasNote)
            }

            UsButton(
                text = if (state.isNew) "Add dish" else "Save changes",
                onClick = viewModel::save,
                loading = state.saving,
                enabled = !state.deleting,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}
