package com.us.android.feature.rider.offers

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.MessagePane
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.contentPadding

@Composable
fun OfferScreen(onBack: () -> Unit, onAccepted: () -> Unit, viewModel: OfferViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(viewModel) {
        viewModel.outcome.collect { outcome ->
            when (outcome) {
                OfferOutcome.Accepted -> onAccepted()
                OfferOutcome.Closed -> onBack()
            }
        }
    }

    RiderScreen(title = "Job offer", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        val offer = state.offer
        when {
            state.loading -> LoadingPane()
            state.gone || offer == null -> MessagePane(
                title = "This offer is no longer available",
                body = "Another rider took it, or its time ran out. Stay online for the next one.",
                icon = UsIcons.Clock,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            else -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(contentPadding(padding)),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                val window = state.window
                RiderCard {
                    Column(modifier = Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(
                            text = when (window) {
                                is OfferWindow.Open -> "${window.remainingSeconds}"
                                OfferWindow.Expired -> "0"
                                OfferWindow.Unknown -> "—"
                            },
                            fontSize = 56.sp,
                            fontWeight = FontWeight.SemiBold,
                            color = if (window is OfferWindow.Open && window.urgent) UsTheme.extended.statusDanger else UsTheme.extended.accentSolid,
                        )
                        Text(
                            text = if (window == OfferWindow.Expired) "Time's up" else "seconds to accept",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                }
                OfferCard(OfferSummary.of(offer))
                InfoNote("The exact drop-off address shows once you accept.")
                UsButton(
                    text = "Accept job",
                    onClick = viewModel::accept,
                    enabled = window.canRespond,
                    loading = state.responding,
                    modifier = Modifier.fillMaxWidth(),
                )
                UsSecondaryButton(text = "Decline", onClick = viewModel::reject, enabled = !state.responding, modifier = Modifier.fillMaxWidth())
            }
        }
    }
}

/** The offer's job detail. Built from [OfferSummary] only, so every arrival path shows the same card. */
@Composable
fun OfferCard(summary: OfferSummary, modifier: Modifier = Modifier) {
    RiderCard(modifier = modifier) {
        CardHeading(summary.title, summary.restaurantArea)
        summary.pay?.let { LabeledValue("You earn", it, emphasise = true) }
        summary.toRestaurant?.let { LabeledValue("To the restaurant", it) }
        summary.tripDistance?.let { LabeledValue("Trip", it) }
        summary.dropLocality?.let { LabeledValue("Drop area", it) }
    }
}
