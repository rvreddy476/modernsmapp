package com.us.android.feature.rider.earnings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.money.RupeeFormat
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderPill
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.SectionHeader
import com.us.android.feature.rider.ui.contentPadding
import com.us.android.feature.rider.ui.datePart
import com.us.android.feature.rider.ui.humanise

@Composable
fun EarningsScreen(onBack: () -> Unit, viewModel: EarningsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    RiderScreen(title = "Earnings", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@RiderScreen
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = contentPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            state.earnings?.let { earnings ->
                item {
                    RiderCard {
                        CardHeading("Today")
                        LabeledValue("Deliveries", earnings.deliveriesToday.toString())
                        LabeledValue("Earned", RupeeFormat.format(earnings.earningsToday), emphasise = true)
                    }
                }
                item {
                    RiderCard {
                        CardHeading("All time")
                        LabeledValue("Deliveries", earnings.totalDeliveries.toString())
                        LabeledValue("Earned", RupeeFormat.format(earnings.totalEarnings), emphasise = true)
                    }
                }
            }
            item { SectionHeader("Past deliveries") }
            if (state.history.isEmpty()) {
                item { InfoNote("Your finished deliveries will be listed here.") }
            }
            items(state.history, key = { it.id }) { job ->
                RiderCard {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CardHeading(job.restaurantName, "${job.orderNumber} · ${datePart(job.createdAt)}", modifier = Modifier.weight(1f))
                        RiderPill(humanise(job.status), if (job.status == "DELIVERED") PillTone.Positive else PillTone.Neutral)
                    }
                    LabeledValue("You earned", RupeeFormat.format(job.deliveryPartnerPayout))
                }
            }
        }
    }
}
