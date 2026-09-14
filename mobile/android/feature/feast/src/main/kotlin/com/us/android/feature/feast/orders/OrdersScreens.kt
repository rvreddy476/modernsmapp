package com.us.android.feature.feast.orders

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.toRupeeText
import com.us.android.core.food.network.FeastInvoiceDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.InvoiceLineDto
import com.us.android.core.food.network.InvoiceSectionDto
import com.us.android.feature.feast.tracking.OrderTimeline
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.InfoNote
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.MoneyRow
import com.us.android.feature.feast.ui.Pill
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone
import com.us.android.feature.feast.ui.formatPlacedAt
import com.us.android.feature.feast.ui.listPadding
import com.us.android.feature.feast.ui.serverTotal

@Composable
fun OrdersScreen(
    onBack: () -> Unit,
    onOpenOrder: (String) -> Unit,
    viewModel: OrdersViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.load()
        onPauseOrDispose { }
    }
    FeastScreen(title = "Your orders", onBack = onBack) { padding ->
        when {
            state.loading -> LoadingPane()
            state.error != null && state.orders.isEmpty() -> MessagePane(
                title = "Couldn't load your orders",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            state.orders.isEmpty() -> MessagePane(
                title = "No orders yet",
                body = "Your Feast orders will show up here.",
                icon = UsIcons.Package,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                items(state.orders, key = { it.id }) { order -> OrderRow(order, onClick = { onOpenOrder(order.id) }) }
            }
        }
    }
}

@Composable
private fun OrderRow(order: FeastOrderDto, onClick: () -> Unit) {
    FeastCard(onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(order.restaurantName, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                Text(
                    "${order.orderNumber} · ${formatPlacedAt(order.placedAt)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
            Text(order.serverTotal().toRupeeText(), style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
        }
        val tone = when {
            order.status == "DELIVERED" -> Tone.Positive
            OrderTimeline.isLive(order.status) -> Tone.Accent
            else -> Tone.Neutral
        }
        Pill(OrderTimeline.label(order.status), tone)
        if (order.items.isNotEmpty()) {
            Text(
                order.items.joinToString(", ") { "${it.quantity} × ${it.name}" },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
                maxLines = 2,
            )
        }
    }
}

@Composable
fun InvoiceScreen(onBack: () -> Unit, viewModel: InvoiceViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    FeastScreen(title = "Invoice", onBack = onBack) { padding ->
        when (val s = state) {
            InvoiceUiState.Loading -> LoadingPane()
            is InvoiceUiState.Failed -> MessagePane(
                title = "Invoice unavailable",
                body = s.message,
                icon = UsIcons.FileText,
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            is InvoiceUiState.Loaded -> InvoiceBody(s.invoice, padding)
        }
    }
}

@Composable
private fun InvoiceBody(invoice: FeastInvoiceDto, padding: androidx.compose.foundation.layout.PaddingValues) {
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = listPadding(padding),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item {
            FeastCard {
                Text("Order ${invoice.orderNumber}", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                Text("Invoice date ${invoice.invoiceDate}", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                val buyer = listOfNotNull(invoice.buyer.name, invoice.buyer.addressLine, invoice.buyer.city, invoice.buyer.state)
                    .filter { it.isNotBlank() }
                    .joinToString(", ")
                if (buyer.isNotBlank()) Text("Billed to $buyer", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        items(invoice.sections, key = { it.invoiceNumber.ifBlank { it.issuer } }) { section -> InvoiceSection(section) }
        item {
            FeastCard {
                MoneyRow("Grand total", invoice.grandTotalPaise, emphasise = true)
                invoice.notes.forEach { InfoNote(it) }
                invoice.adviserMarker?.takeIf { invoice.needsAdviserConfirmation && it.isNotBlank() }?.let { InfoNote(it) }
            }
        }
    }
}

@Composable
private fun InvoiceSection(section: InvoiceSectionDto) {
    Column {
        SectionLabel(section.title)
        FeastCard {
            Text(section.issuerName, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
            Text(
                listOfNotNull(section.invoiceNumber.ifBlank { null }, section.issuerGstin?.let { "GSTIN $it" }).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            section.lines.forEach { InvoiceLine(it) }
            HorizontalDivider(color = UsTheme.extended.borderSubtle, modifier = Modifier.padding(vertical = 4.dp))
            MoneyRow("Taxable value", section.taxablePaise, muted = true)
            if (section.cgstPaise.value > 0 || section.sgstPaise.value > 0) {
                MoneyRow("CGST", section.cgstPaise, muted = true)
                MoneyRow("SGST", section.sgstPaise, muted = true)
            }
            if (section.igstPaise.value > 0) MoneyRow("IGST", section.igstPaise, muted = true)
            MoneyRow("Total", section.totalPaise, emphasise = true)
            section.notes.forEach { InfoNote(it) }
        }
    }
}

@Composable
private fun InvoiceLine(line: InvoiceLineDto) {
    Column(Modifier.padding(vertical = 2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                "${line.quantity} × ${line.description}",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.weight(1f),
            )
            Text(line.totalPaise.toRupeeText(), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary, fontWeight = FontWeight.Medium)
        }
        Text(
            "Taxable ${line.taxablePaise.toRupeeText()} · GST ${line.ratePercent}% ${line.taxPaise.toRupeeText()}" +
                (line.sac?.let { " · SAC $it" } ?: ""),
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textDim,
        )
    }
}
