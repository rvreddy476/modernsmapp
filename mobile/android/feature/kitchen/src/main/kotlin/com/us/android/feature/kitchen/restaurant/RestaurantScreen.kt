package com.us.android.feature.kitchen.restaurant

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.feature.kitchen.money.RupeeFormat
import com.us.android.feature.kitchen.onboarding.StepEditor
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LabeledValue
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.MessagePane
import com.us.android.feature.kitchen.ui.NavRow
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.RestaurantStatusText
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.kitchenSwitchColors
import com.us.android.feature.kitchen.ui.listPadding

@Composable
fun RestaurantScreen(
    restaurants: List<PartnerRestaurantDto>,
    onOpenOnboarding: () -> Unit,
    onOpenStep: (StepEditor) -> Unit,
    onSelectRestaurant: (String) -> Unit,
    onRestaurantChanged: () -> Unit,
    onSignOut: () -> Unit,
    bottomBar: @Composable () -> Unit,
    viewModel: RestaurantViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.restaurantChanges) {
        if (state.restaurantChanges > 0) onRestaurantChanged()
    }
    LifecycleResumeEffect(Unit) {
        viewModel.load()
        onPauseOrDispose { }
    }
    KitchenScreen(
        title = "Kitchen",
        onBack = null,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = bottomBar,
    ) { padding ->
        val restaurant = state.restaurant
        when {
            restaurant == null && state.loading -> LoadingPane(Modifier.padding(padding))
            restaurant == null -> MessagePane(
                title = "Couldn't load your kitchen",
                body = state.loadError.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
                secondaryLabel = "Sign out",
                onSecondary = onSignOut,
                modifier = Modifier.padding(padding),
            )
            else -> LazyColumn(
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                item(key = "header") {
                    KitchenCard {
                        Text(restaurant.name, style = MaterialTheme.typography.headlineSmall, color = UsTheme.extended.textPrimary)
                        Row(
                            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            KitchenPill(RestaurantStatusText.label(restaurant.status), RestaurantStatusText.tone(restaurant.status))
                            Text(
                                text = listOfNotNull(restaurant.city.ifBlank { null }, restaurant.state).joinToString(", "),
                                style = MaterialTheme.typography.bodySmall,
                                color = UsTheme.extended.textMuted,
                            )
                        }
                    }
                }
                item(key = "accepting") {
                    AcceptingCard(restaurant = restaurant, busy = state.acceptingBusy, onToggle = viewModel::setAccepting)
                }

                item(key = "h-setup") { SectionHeader("Setup") }
                item(key = "checklist") {
                    NavRow(title = "Setup checklist", detail = "See what Feast still needs", onClick = onOpenOnboarding, icon = UsIcons.Check)
                }
                items(SETUP_ROWS, key = { "setup-${it.editor.name}" }) { row ->
                    NavRow(title = row.title, detail = row.detail, onClick = { onOpenStep(row.editor) }, icon = row.icon)
                }

                item(key = "h-basics") { SectionHeader("Basics") }
                item(key = "basics") {
                    KitchenCard {
                        LabeledValue("Minimum order", RupeeFormat.format(restaurant.minOrderAmount))
                        LabeledValue("Packaging fee", RupeeFormat.format(restaurant.packagingFee))
                        if (restaurant.slug.isNotBlank()) LabeledValue("Listing name", restaurant.slug)
                        InfoNote("Changing these isn't available in the app yet.")
                    }
                }

                if (restaurants.size > 1) {
                    item(key = "h-switch") { SectionHeader("Your restaurants") }
                    items(restaurants, key = { "restaurant-${it.id}" }) { other ->
                        NavRow(
                            title = other.name,
                            detail = RestaurantStatusText.label(other.status) + if (other.id == restaurant.id) " · showing now" else "",
                            onClick = { onSelectRestaurant(other.id) },
                        )
                    }
                }

                item(key = "sign-out") {
                    UsSecondaryButton(text = "Sign out", onClick = onSignOut, modifier = Modifier.fillMaxWidth())
                }
            }
        }
    }
}

@Composable
private fun AcceptingCard(restaurant: PartnerRestaurantDto, busy: Boolean, onToggle: (Boolean) -> Unit) {
    val live = restaurant.status == RestaurantStatusText.ACTIVE
    KitchenCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(
                title = if (restaurant.isAcceptingOrders) "Taking orders" else "Orders paused",
                body = if (restaurant.isAcceptingOrders) {
                    "Customers can order from you now."
                } else {
                    "Customers can't order until you turn this on."
                },
                modifier = Modifier.weight(1f),
            )
            Switch(
                checked = restaurant.isAcceptingOrders,
                onCheckedChange = onToggle,
                enabled = live && !busy,
                colors = kitchenSwitchColors(),
            )
        }
        if (!live) InfoNote("You can take orders once Feast approves your kitchen.", tone = PillTone.Warning)
    }
}

private data class SetupRow(val editor: StepEditor, val title: String, val detail: String, val icon: ImageVector)

private val SETUP_ROWS = listOf(
    SetupRow(StepEditor.LOCATION, "Location", "Address and delivery radius", UsIcons.MapPin),
    SetupRow(StepEditor.OPERATING_HOURS, "Opening hours", "When you take orders", UsIcons.Gauge),
    SetupRow(StepEditor.COMPLIANCE, "Tax details", "Category, PAN and GSTIN", UsIcons.FileText),
    SetupRow(StepEditor.FSSAI, "FSSAI licence", "Licence number and photo", UsIcons.Tag),
    SetupRow(StepEditor.PAYOUT, "Bank account", "Where your earnings are paid", UsIcons.CreditCard),
    SetupRow(StepEditor.MENU, "Menu", "Dishes, prices and stock", UsIcons.Utensils),
)
