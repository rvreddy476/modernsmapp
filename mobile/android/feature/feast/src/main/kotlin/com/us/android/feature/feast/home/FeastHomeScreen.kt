package com.us.android.feature.feast.home

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.toRupeeText
import com.us.android.feature.feast.restaurant.Serviceability
import com.us.android.feature.feast.tracking.OrderTimeline
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.Pill
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone
import com.us.android.feature.feast.ui.listPadding

@Composable
@Suppress("LongMethod", "LongParameterList")
fun FeastHomeScreen(
    onBack: () -> Unit,
    onOpenRestaurant: (String) -> Unit,
    onOpenCart: () -> Unit,
    onOpenAddresses: () -> Unit,
    onOpenOrders: () -> Unit,
    onTrackOrder: (String) -> Unit,
    viewModel: FeastHomeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.refresh()
        onPauseOrDispose { }
    }

    FeastScreen(
        title = "Feast",
        onBack = onBack,
        actions = {
            IconButton(onClick = onOpenOrders) {
                Icon(UsIcons.Package, contentDescription = "Your orders", tint = UsTheme.extended.textPrimary)
            }
        },
        bottomBar = {
            val cart = state.cart
            if (cart != null && state.cartItemCount > 0) {
                CartBar(
                    itemCount = state.cartItemCount,
                    restaurant = cart.restaurant.orEmpty(),
                    total = state.cartBill?.total?.toRupeeText(),
                    onClick = onOpenCart,
                )
            }
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = listPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item {
                Column {
                    Text(
                        text = "Good food, delivered",
                        style = MaterialTheme.typography.headlineSmall.copy(fontFamily = MomentumWordmarkFontFamily),
                        color = UsTheme.extended.textPrimary,
                    )
                    DeliverTo(
                        label = state.address?.let { it.label?.ifBlank { null } ?: "Deliver to" } ?: "Add a delivery address",
                        line = state.address?.let { listOfNotNull(it.addressLine1, it.city).joinToString(", ") },
                        onClick = onOpenAddresses,
                        modifier = Modifier.padding(top = UsTheme.spacing.l),
                    )
                }
            }
            item {
                UsTextField(
                    value = state.query,
                    onValueChange = viewModel::onQueryChange,
                    label = "Search restaurants and cuisines",
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            state.liveOrder?.let { order ->
                item {
                    FeastCard(onClick = { onTrackOrder(order.id) }) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(
                                modifier = Modifier.size(40.dp).background(UsTheme.extended.ctaGradient, RoundedCornerShape(12.dp)),
                                contentAlignment = Alignment.Center,
                            ) {
                                Icon(UsIcons.Utensils, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(20.dp))
                            }
                            Spacer(Modifier.width(UsTheme.spacing.l))
                            Column(Modifier.weight(1f)) {
                                Text(
                                    text = OrderTimeline.label(order.status),
                                    style = MaterialTheme.typography.titleMedium,
                                    color = UsTheme.extended.textPrimary,
                                )
                                Text(
                                    text = "${order.restaurantName} · Track your order",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = UsTheme.extended.textMuted,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                            Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textDim)
                        }
                    }
                }
            }
            item { SectionLabel(if (state.query.isBlank()) "Restaurants near you" else "Results") }
            when {
                state.loading -> item { LoadingPane(Modifier.padding(top = 48.dp)) }
                state.error != null && state.restaurants.isEmpty() -> item {
                    MessagePane(
                        title = "Couldn't load restaurants",
                        body = state.error.orEmpty(),
                        primaryLabel = "Try again",
                        onPrimary = viewModel::refresh,
                        modifier = Modifier.padding(top = 24.dp),
                    )
                }
                state.restaurants.isEmpty() -> item {
                    MessagePane(
                        title = "No restaurants yet",
                        body = if (state.query.isBlank()) "Feast isn't delivering here yet." else "Nothing matches \"${state.query}\".",
                        icon = UsIcons.Store,
                        modifier = Modifier.padding(top = 24.dp),
                    )
                }
                else -> items(state.restaurants, key = { it.restaurant.id }) { row ->
                    RestaurantCard(row = row, onClick = { onOpenRestaurant(row.restaurant.id) })
                }
            }
        }
    }
}

@Composable
private fun DeliverTo(label: String, line: String?, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.panel))
            .clickable(onClick = onClick)
            .padding(vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(UsTheme.spacing.m))
        Column(Modifier.weight(1f)) {
            Text(label, style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textPrimary, fontWeight = FontWeight.SemiBold)
            if (line != null) {
                Text(line, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        Icon(UsIcons.ChevronDown, contentDescription = "Change address", tint = UsTheme.extended.textDim)
    }
}

@Composable
private fun RestaurantCard(row: RestaurantRow, onClick: () -> Unit) {
    val r = row.restaurant
    val blocked = row.serviceability as? Serviceability.Blocked
    FeastCard(onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                modifier = Modifier
                    .size(56.dp)
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(if (blocked == null) UsTheme.extended.ctaGradient else UsTheme.extended.launcher.feast.brush)
                    .border(1.dp, UsTheme.extended.borderSubtle, RoundedCornerShape(UsTheme.radii.medium)),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = r.name.take(1).uppercase(),
                    style = MaterialTheme.typography.headlineSmall.copy(fontFamily = MomentumWordmarkFontFamily),
                    color = UsTheme.extended.textPrimary,
                )
            }
            Spacer(Modifier.width(UsTheme.spacing.l))
            Column(Modifier.weight(1f)) {
                Text(r.name, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary, maxLines = 1, overflow = TextOverflow.Ellipsis)
                val cuisines = r.cuisines.joinToString(" · ").ifBlank { r.city }
                Text(cuisines, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(
                    modifier = Modifier.padding(top = UsTheme.spacing.s),
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    if (blocked != null) {
                        Pill(if (r.isOpen) "Not accepting orders" else "Closed", Tone.Danger)
                    } else {
                        Pill("Open", Tone.Positive)
                    }
                    if (r.ratingCount > 0) {
                        Text("★ ${r.avgRating.setScale(1, java.math.RoundingMode.HALF_UP).toPlainString()}", style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textSecondary)
                    }
                    val eta = r.estimatedDelivery.ifBlank { if (r.avgPreparationMinutes > 0) "${r.avgPreparationMinutes} min" else "" }
                    if (eta.isNotBlank()) {
                        Text(eta, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textSecondary)
                    }
                }
            }
        }
    }
}

@Composable
internal fun CartBar(itemCount: Int, restaurant: String, total: String?, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCanvas)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.ctaGradient)
            .clickable(onClick = onClick)
            .padding(horizontal = 20.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(UsIcons.ShoppingCart, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(UsTheme.spacing.l))
        Column(Modifier.weight(1f)) {
            Text(
                text = if (itemCount == 1) "1 item" else "$itemCount items",
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
            if (restaurant.isNotBlank()) {
                Text(restaurant, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textPrimary.copy(alpha = 0.8f), maxLines = 1)
            }
        }
        Text(
            text = total?.let { "$it  ·  View cart" } ?: "View cart",
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
            fontWeight = FontWeight.SemiBold,
        )
    }
}
