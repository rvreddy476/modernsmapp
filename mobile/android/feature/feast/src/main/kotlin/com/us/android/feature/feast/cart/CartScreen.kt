package com.us.android.feature.feast.cart

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.toRupeeText
import com.us.android.core.food.network.FeastCartItemDto
import com.us.android.feature.feast.ui.BottomAction
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.FoodTypeMark
import com.us.android.feature.feast.ui.InfoNote
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.MoneyRow
import com.us.android.feature.feast.ui.QuantityStepper
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone
import com.us.android.feature.feast.ui.listPadding

@Composable
fun CartScreen(
    onBack: () -> Unit,
    onBrowse: () -> Unit,
    onCheckout: () -> Unit,
    viewModel: CartViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.load()
        onPauseOrDispose { }
    }
    val bill = state.bill

    FeastScreen(
        title = "Your cart",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (bill != null && !state.isEmpty) {
                BottomAction(
                    label = bill.total?.let { "Checkout · ${it.toRupeeText()}" } ?: "Checkout",
                    onClick = onCheckout,
                    enabled = bill.payable && state.updatingLineId == null,
                    summary = bill.blockedReason,
                )
            }
        },
    ) { padding ->
        val cart = state.cart
        when {
            state.loading -> LoadingPane()
            cart == null -> MessagePane(
                title = "Couldn't load your cart",
                body = state.loadError.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            state.isEmpty -> MessagePane(
                title = "Your cart is empty",
                body = "Add something delicious from a restaurant near you.",
                icon = UsIcons.ShoppingCart,
                primaryLabel = "Browse restaurants",
                onPrimary = onBrowse,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                item { SectionLabel(cart.restaurant ?: "Items") }
                items(cart.items, key = { it.id }) { line ->
                    CartLine(
                        line = line,
                        updating = state.updatingLineId == line.id,
                        onQuantity = { q -> viewModel.changeQuantity(line.id, q) },
                    )
                }
                if (bill != null) item { BillCard(bill) }
            }
        }
    }
}

@Composable
private fun CartLine(line: FeastCartItemDto, updating: Boolean, onQuantity: (Int) -> Unit) {
    FeastCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            FoodTypeMark(line.foodType)
            Spacer(Modifier.width(UsTheme.spacing.m))
            Column(Modifier.weight(1f)) {
                Text(line.name, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                if (line.addons.isNotEmpty()) {
                    Text(
                        text = line.addons.joinToString(", ") { it.name },
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
                // The server's own line figure. Add-ons are their own server figure.
                Text(line.lineTotalPaise.toRupeeText(), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
            }
            QuantityStepper(
                quantity = line.quantity,
                onDecrease = { onQuantity(line.quantity - 1) },
                onIncrease = { onQuantity(line.quantity + 1) },
                enabled = !updating,
            )
        }
    }
}

/** The bill, line for line as the server priced it. See [CartBill]. */
@Composable
internal fun BillCard(bill: CartBill, title: String = "Bill details") {
    FeastCard {
        Text(title, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        bill.itemTotal?.let { MoneyRow("Item total", it) }
        bill.addonTotal?.let { MoneyRow("Add-ons", it) }
        bill.charges.forEach { MoneyRow(it.label, it.amount) }
        if (bill.taxes.isNotEmpty()) {
            Text(
                "Taxes",
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textDim,
                modifier = Modifier.padding(top = UsTheme.spacing.s),
            )
            bill.taxes.forEach { MoneyRow(it.label, it.amount, muted = true) }
        }
        bill.discount?.let { MoneyRow("Discount", it) }
        HorizontalDivider(color = UsTheme.extended.borderSubtle, modifier = Modifier.padding(vertical = 4.dp))
        bill.total?.let { MoneyRow("To pay", it, emphasise = true) }
        bill.blockedReason?.let { InfoNote(it, tone = Tone.Danger) }
        bill.adviserNotice?.let { InfoNote(it) }
    }
}
