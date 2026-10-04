package com.us.android.feature.doorstep.professionals

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.data.ProfessionalCardDto
import com.us.android.feature.doorstep.domain.BookingMode
import com.us.android.feature.doorstep.domain.ChangeMoney
import com.us.android.feature.doorstep.domain.ProfessionalListView
import com.us.android.feature.doorstep.domain.ProfessionalPick
import com.us.android.feature.doorstep.domain.ProfessionalRules
import com.us.android.feature.doorstep.domain.ProfessionalSort
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.ui.ChoiceDeadline
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.MoneyRow
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.listPadding
import com.us.android.feature.doorstep.ui.momentText

/** Server-priced choices. No automatic reassignment, invented availability or client total. */
@Composable
fun ProfessionalsScreen(
    onBack: () -> Unit,
    onCheckout: (ProfessionalsOutcome.Checkout) -> Unit,
    onMoreTimes: (ProfessionalsOutcome.MoreTimes) -> Unit,
    onBackToBooking: (String) -> Unit,
    onOpenOutstanding: () -> Unit,
    onStartAgain: () -> Unit,
    viewModel: ProfessionalsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.outcome) {
        when (val outcome = state.outcome) {
            is ProfessionalsOutcome.Checkout -> onCheckout(outcome)
            is ProfessionalsOutcome.MoreTimes -> onMoreTimes(outcome)
            is ProfessionalsOutcome.BackToBooking -> onBackToBooking(outcome.bookingId)
            null -> return@LaunchedEffect
        }
        viewModel.consumeOutcome()
    }
    DoorstepScreen("Choose your professional", onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        when {
            state.blockedByDues -> MessagePane("An earlier bill needs attention", "Clear your outstanding balance to book a new visit.", primaryLabel = "View balance", onPrimary = onOpenOutstanding)
            state.draftLost -> MessagePane("Choose your service again", "Your selection wasn't saved. No booking or payment was made.", primaryLabel = "Browse services", onPrimary = onStartAgain)
            state.choiceClosed != null -> MessagePane("This booking has changed", state.choiceClosed.orEmpty(), primaryLabel = "Back to booking", onPrimary = { state.booking?.let { onBackToBooking(it.id) } ?: onBack() })
            state.applied != null -> MessagePane("Professional selected", state.applied?.let { "${it.proFirstName} is your new professional." + if (it.refund.value > 0) " ${it.refund.toRupeeText()} is being refunded." else " Your payment stays the same." }.orEmpty(), primaryLabel = "View booking", onPrimary = { state.booking?.let { onBackToBooking(it.id) } })
            state.loading && state.list == null -> LoadingPane(label = "Finding approved professionals")
            state.error != null && state.list == null -> MessagePane("Couldn't load professionals", state.error.orEmpty(), primaryLabel = "Try again", onPrimary = viewModel::load)
            else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                item {
                    Column(Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                        Text(state.serviceName ?: "Your visit", style = MaterialTheme.typography.headlineSmall, color = UsTheme.extended.textPrimary)
                        Text("Compare approved prices and choose who comes to your door.", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                        state.booking?.let { ChoiceDeadline(it, onLapsed = viewModel::load) }
                        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                            FilterChip(state.mode == BookingMode.SCHEDULED, { viewModel.setMode(BookingMode.SCHEDULED) }, label = { Text("Scheduled") })
                            FilterChip(state.mode == BookingMode.ASAP, { viewModel.setMode(BookingMode.ASAP) }, label = { Text("As soon as possible") })
                        }
                        LazyRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                            items(ProfessionalSort.entries) { sort -> FilterChip(state.sort == sort, { viewModel.setSort(sort) }, label = { Text(sort.label) }) }
                        }
                        if (state.loading) InfoNote("Refreshing availability…")
                        state.error?.let { InfoNote(it, tone = Tone.Warning) }
                    }
                }
                val view = state.view
                val cards = when (view) {
                    is ProfessionalListView.Cards -> view.cards
                    is ProfessionalListView.AsapNobody -> view.alternatives
                    else -> emptyList()
                }
                val mode = (view as? ProfessionalListView.Cards)?.mode ?: BookingMode.SCHEDULED
                if (view is ProfessionalListView.AsapNobody) item {
                    DoorstepCard(Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
                        Text("Nobody is available immediately", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                        InfoNote("Your service and address are kept. Choose one of these scheduled times instead.")
                    }
                }
                if (view is ProfessionalListView.Empty) item {
                    DoorstepCard(Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
                        Text("No available professionals", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                        InfoNote("Try scheduled visits or refresh availability. You won't be charged.")
                        UsSecondaryButton("Check scheduled visits", { viewModel.setMode(BookingMode.SCHEDULED) })
                    }
                }
                items(cards, key = { it.proId }) { card ->
                    ProfessionalCard(card, mode, state.picking != null, state.forBooking, { viewModel.pick(card, it) }, { viewModel.moreTimes(card) })
                }
            }
        }
    }
}

@Composable
private fun ProfessionalCard(card: ProfessionalCardDto, mode: BookingMode, busy: Boolean, changing: Boolean, onPick: (ProfessionalPick) -> Unit, onMore: () -> Unit) {
    DoorstepCard(Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
        Text(card.firstName, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
        Text("${ProfessionalRules.ratingText(card)} · ${card.jobsCompleted} completed visits", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        ProfessionalRules.distanceText(card.distanceBand)?.let { InfoNote(it) }
        MoneyRow("Visit total · tax included", Paise(card.price.totalPaise), emphasise = true)
        when (val money = ChangeMoney.of(card.differencePaise)) {
            is ChangeMoney.Refund -> InfoNote("${money.amount.toRupeeText()} will be refunded", tone = Tone.Positive)
            is ChangeMoney.Charge -> InfoNote("Pay ${money.amount.toRupeeText()} extra to confirm", tone = Tone.Warning)
            ChangeMoney.Same -> InfoNote("No extra payment")
            null -> Unit
        }
        ProfessionalRules.picks(card, mode).forEach { pick ->
            val label = when (pick) {
                is ProfessionalPick.Asap -> ProfessionalRules.etaText(pick.etaMinutes)
                is ProfessionalPick.At -> momentText(pick.slotStart)
            }
            UsButton(label, { onPick(pick) }, enabled = !busy, modifier = Modifier.fillMaxWidth())
        }
        if (!changing && mode == BookingMode.SCHEDULED && card.nextSlots.isNotEmpty()) UsSecondaryButton("More times", onMore, enabled = !busy, modifier = Modifier.fillMaxWidth())
    }
}
