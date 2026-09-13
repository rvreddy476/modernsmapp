package com.us.android.feature.kitchen.menu

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.MenuItemDto
import com.us.android.feature.kitchen.money.RupeeFormat
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.MessagePane
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.kitchenSwitchColors
import com.us.android.feature.kitchen.ui.listPadding

@Composable
fun MenuScreen(
    onBack: (() -> Unit)?,
    onOpenItem: (categoryId: String, itemId: String?) -> Unit,
    bottomBar: @Composable () -> Unit = {},
    viewModel: MenuViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // Re-read on every return, including from the dish editor.
    LifecycleResumeEffect(Unit) {
        viewModel.refresh()
        onPauseOrDispose { }
    }
    state.categoryDraft?.let { draft ->
        AddCategoryDialog(
            draft = draft,
            adding = state.addingCategory,
            onDraftChange = viewModel::onCategoryDraft,
            onConfirm = viewModel::addCategory,
            onDismiss = viewModel::closeAddCategory,
        )
    }
    KitchenScreen(
        title = "Menu",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = bottomBar,
        actions = {
            IconButton(onClick = viewModel::openAddCategory) {
                Icon(UsIcons.Create, contentDescription = "Add a category", tint = UsTheme.extended.textPrimary)
            }
        },
    ) { padding ->
        when {
            state.categories.isEmpty() && state.loading -> LoadingPane(Modifier.padding(padding))
            state.categories.isEmpty() && state.loadError != null -> MessagePane(
                title = "Couldn't load your menu",
                body = state.loadError.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
                modifier = Modifier.padding(padding),
            )
            state.categories.isEmpty() -> MessagePane(
                title = "Start your menu",
                body = "Add a category such as Starters or Mains, then add dishes to it.",
                icon = UsIcons.Utensils,
                primaryLabel = "Add a category",
                onPrimary = viewModel::openAddCategory,
                modifier = Modifier.padding(padding),
            )
            else -> LazyColumn(
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                item(key = "note") {
                    InfoNote("Switch a dish off when it runs out — customers stop seeing it at once. Switch it back on when it's ready.")
                }
                state.categories.forEach { category ->
                    item(key = "category-${category.id}") {
                        SectionHeader(
                            title = category.name,
                            trailing = {
                                if (category.items.isEmpty()) {
                                    IconButton(onClick = { viewModel.deleteCategory(category.id) }) {
                                        Icon(UsIcons.Trash, contentDescription = "Remove ${category.name}", tint = UsTheme.extended.textDim)
                                    }
                                }
                                TextButton(onClick = { onOpenItem(category.id, null) }) {
                                    Text("Add dish", color = UsTheme.extended.accentSolid)
                                }
                            },
                        )
                    }
                    if (category.items.isEmpty()) {
                        item(key = "empty-${category.id}") {
                            Text("No dishes yet.", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                    }
                    items(category.items, key = { "item-${it.id}" }) { item ->
                        MenuItemRow(
                            item = item,
                            busy = item.id in state.busyItemIds,
                            onToggle = { viewModel.setAvailability(item.id, it) },
                            onClick = { onOpenItem(category.id, item.id) },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun MenuItemRow(item: MenuItemDto, busy: Boolean, onToggle: (Boolean) -> Unit, onClick: () -> Unit) {
    KitchenCard(onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            FoodTypeMark(item.foodType)
            Column(
                modifier = Modifier
                    .weight(1f)
                    .padding(horizontal = UsTheme.spacing.l),
            ) {
                Text(
                    text = item.name,
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                PriceLine(item)
                if (!item.isAvailable) {
                    Text("Out of stock", style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.statusDanger)
                }
            }
            Switch(
                checked = item.isAvailable,
                onCheckedChange = onToggle,
                enabled = !busy,
                colors = kitchenSwitchColors(),
            )
        }
    }
}

/** The Indian veg / non-veg / egg mark: a square with a dot, in the status colours. */
@Composable
internal fun FoodTypeMark(foodType: String, modifier: Modifier = Modifier) {
    val (color, label) = when (foodType) {
        FOOD_VEG -> UsTheme.extended.statusSuccess to "Vegetarian"
        FOOD_VEGAN -> UsTheme.extended.statusSuccess to "Vegan"
        FOOD_JAIN -> UsTheme.extended.statusSuccess to "Jain"
        FOOD_EGG -> UsTheme.extended.statusWarning to "Contains egg"
        else -> UsTheme.extended.statusDanger to "Non-vegetarian"
    }
    Box(
        modifier = modifier
            .size(18.dp)
            .border(1.5.dp, color, RoundedCornerShape(3.dp))
            .semantics { contentDescription = label },
        contentAlignment = Alignment.Center,
    ) {
        Box(Modifier.size(8.dp).background(color, CircleShape))
    }
}

@Composable
internal fun PriceLine(item: MenuItemDto) {
    val offer = item.discountPrice?.takeIf { it.value > 0 && it < item.basePrice }
    Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = RupeeFormat.format(offer ?: item.basePrice),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        if (offer != null) {
            Text(
                text = RupeeFormat.format(item.basePrice),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textDim,
                textDecoration = TextDecoration.LineThrough,
            )
        }
    }
}

@Composable
private fun AddCategoryDialog(
    draft: String,
    adding: Boolean,
    onDraftChange: (String) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("New category") },
        text = { UsTextField(value = draft, onValueChange = onDraftChange, label = "Name", placeholder = "Starters") },
        confirmButton = {
            TextButton(onClick = onConfirm, enabled = draft.isNotBlank() && !adding) {
                Text(if (adding) "Adding…" else "Add", color = UsTheme.extended.accentSolid)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Cancel", color = UsTheme.extended.textMuted) }
        },
        containerColor = UsTheme.extended.bgRaised,
        titleContentColor = UsTheme.extended.textPrimary,
    )
}

/** food-service `food.food_type` (database/setup.sql). */
internal const val FOOD_VEG = "VEG"
internal const val FOOD_NON_VEG = "NON_VEG"
internal const val FOOD_EGG = "EGG"
internal const val FOOD_VEGAN = "VEGAN"
internal const val FOOD_JAIN = "JAIN"
