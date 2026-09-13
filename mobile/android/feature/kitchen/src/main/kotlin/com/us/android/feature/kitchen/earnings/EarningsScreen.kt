package com.us.android.feature.kitchen.earnings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
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
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.RestaurantSummaryDto
import com.us.android.core.food.network.SettlementDto
import com.us.android.feature.kitchen.money.RupeeFormat
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LabeledValue
import com.us.android.feature.kitchen.ui.LoadingPane
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.datePart
import com.us.android.feature.kitchen.ui.humanise
import com.us.android.feature.kitchen.ui.listPadding

@Composable
fun EarningsScreen(bottomBar: @Composable () -> Unit, viewModel: EarningsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(Unit) {
        viewModel.load()
        onPauseOrDispose { }
    }
    KitchenScreen(title = "Earnings", onBack = null, bottomBar = bottomBar) { padding ->
        if (state.loading) {
            LoadingPane(Modifier.padding(padding))
            return@KitchenScreen
        }
        LazyColumn(
            contentPadding = listPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item(key = "summary") {
                val summary = state.summary
                if (summary != null) {
                    SummaryCard(summary)
                } else {
                    KitchenCard { CardHeading("Earnings aren't available", state.summaryError) }
                }
            }
            item(key = "h-settlements") { SectionHeader("Settlements") }
            state.settlementsError?.let { error -> item(key = "settlements-error") { InfoNote(error, tone = PillTone.Danger) } }
            if (state.settlements.isEmpty() && state.settlementsError == null) {
                item(key = "settlements-empty") {
                    Text(
                        text = "No settlements yet. Each payout period appears here once Feast settles it.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }
            items(state.settlements) { SettlementCard(it) }
        }
    }
}

@Composable
private fun SummaryCard(summary: RestaurantSummaryDto) {
    KitchenCard {
        Text("Payout to date", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        Text(
            text = RupeeFormat.format(summary.payoutAmount),
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        LabeledValue("Order value", RupeeFormat.format(summary.grossAmount))
        LabeledValue("Feast commission", deduction(summary.commission))
        LabeledValue("Refunds", deduction(summary.refunds))
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), verticalAlignment = Alignment.CenterVertically) {
            KitchenPill("${summary.orders} orders", PillTone.Neutral)
            KitchenPill("${summary.delivered} delivered", PillTone.Positive)
            if (summary.refunded > 0) KitchenPill("${summary.refunded} refunded", PillTone.Danger)
        }
    }
}

@Composable
private fun SettlementCard(settlement: SettlementDto) {
    KitchenCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(
                title = "${datePart(settlement.periodStart)} – ${datePart(settlement.periodEnd)}",
                modifier = Modifier.weight(1f),
            )
            val (label, tone) = when (settlement.status) {
                "PAID" -> "Paid" to PillTone.Positive
                "PENDING", "GENERATED" -> "Pending" to PillTone.Warning
                else -> humanise(settlement.status) to PillTone.Neutral
            }
            KitchenPill(label, tone)
        }
        LabeledValue("Payout", RupeeFormat.format(settlement.payoutAmount), emphasise = true)
        LabeledValue("Order value", RupeeFormat.format(settlement.grossAmount))
        LabeledValue("Commission", deduction(settlement.commission))
        if (settlement.refundAdjustment.value != 0L) LabeledValue("Refunds", deduction(settlement.refundAdjustment))
        if (settlement.penaltyAmount.value != 0L) LabeledValue("Penalties", deduction(settlement.penaltyAmount))
        settlement.paidReference?.takeIf { it.isNotBlank() }?.let { LabeledValue("Reference", it) }
    }
}

private fun deduction(amount: Paise): String =
    if (amount.value == 0L) RupeeFormat.format(amount) else "-${RupeeFormat.format(amount)}"
