package com.us.android.feature.doorstep.booking

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.data.SlotDayDto
import com.us.android.feature.doorstep.data.SlotDto
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.ui.BottomAction
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.IST
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.MoneyRow
import com.us.android.feature.doorstep.ui.SectionLabel
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.dayChip
import com.us.android.feature.doorstep.ui.durationText
import com.us.android.feature.doorstep.ui.slotTimeText
import java.time.LocalDate

@Composable
@Suppress("LongMethod")
fun SlotPickerScreen(
    onBack: () -> Unit,
    onCheckout: (SlotOutcome.Checkout) -> Unit,
    onRescheduled: () -> Unit,
    onOpenOutstanding: () -> Unit,
    onStartAgain: () -> Unit,
    viewModel: SlotPickerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.outcome) {
        when (val outcome = state.outcome) {
            is SlotOutcome.Checkout -> onCheckout(outcome)
            SlotOutcome.Rescheduled -> onRescheduled()
            null -> return@LaunchedEffect
        }
        viewModel.consumeOutcome()
    }
    val today = remember { LocalDate.now(IST) }

    DoorstepScreen(
        title = if (state.rescheduling) "Reschedule" else "Pick a slot",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.days.isNotEmpty()) {
                BottomAction(
                    label = if (state.rescheduling) "Move my booking" else "Continue to pay",
                    onClick = viewModel::confirm,
                    enabled = state.selectedSlot != null && !state.submitting,
                    loading = state.submitting,
                    summary = state.selectedSlot?.let { "Selected: ${slotTimeText(it.start)}" },
                )
            }
        },
    ) { padding ->
        when {
            state.loading -> LoadingPane(label = if (state.rescheduling) null else "Pricing your service…")
            state.draftLost -> MessagePane(
                title = "Let's start again",
                body = "We lost your choices while the app was in the background. Pick the service once more.",
                primaryLabel = "Back to services",
                onPrimary = onStartAgain,
            )
            state.blockedByDues -> MessagePane(
                title = "Pending dues",
                body = "Pay your unpaid extras from an earlier visit to book again.",
                icon = UsIcons.CreditCard,
                primaryLabel = "Pay dues",
                onPrimary = onOpenOutstanding,
            )
            state.error != null && state.days.isEmpty() -> MessagePane(
                title = "Couldn't load slots",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            else -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 24.dp),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                state.quote?.let { quote ->
                    DoorstepCard {
                        MoneyRow("Total (GST included)", Paise(quote.totalPaise), emphasise = true)
                        Text(
                            "${durationText(quote.durationMinutes)} · ${state.address?.locality.orEmpty()}",
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                }
                SectionLabel("Day")
                DateStrip(days = state.days, selected = state.selectedDate, today = today, onSelect = viewModel::selectDate)
                SectionLabel("Time")
                if (state.slotsOfDay.none { it.available }) {
                    InfoNote("No professional is free this day. Try another day.", tone = Tone.Warning)
                }
                SlotGrid(slots = state.slotsOfDay, selected = state.selectedSlot, onSelect = viewModel::selectSlot)
                if (!state.rescheduling) {
                    InfoNote("We hold a professional for 10 minutes while you pay. Earliest slot is 2 hours from now.")
                } else {
                    InfoNote("Rescheduling is free once, up to 3 hours before your slot.")
                }
            }
        }
    }
}

@Composable
private fun DateStrip(days: List<SlotDayDto>, selected: String?, today: LocalDate, onSelect: (String) -> Unit) {
    LazyRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), contentPadding = PaddingValues(0.dp)) {
        items(days, key = { it.date }) { day ->
            val (name, number) = dayChip(day.date, today)
            val isSelected = day.date == selected
            val open = day.slots.any { it.available }
            val shape = RoundedCornerShape(UsTheme.radii.medium)
            Column(
                modifier = Modifier
                    .width(DAY_WIDTH)
                    .clip(shape)
                    .background(if (isSelected) UsTheme.extended.accentSolid else UsTheme.extended.bgCardSolid)
                    .border(1.dp, if (isSelected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle, shape)
                    .clickable { onSelect(day.date) }
                    .padding(vertical = UsTheme.spacing.l),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                val ink = when {
                    isSelected -> UsTheme.extended.onAccent
                    open -> UsTheme.extended.textPrimary
                    else -> UsTheme.extended.textDim
                }
                Text(name, style = MaterialTheme.typography.labelSmall, color = ink)
                Text(number, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = ink)
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun SlotGrid(slots: List<SlotDto>, selected: SlotDto?, onSelect: (SlotDto) -> Unit) {
    FlowRow(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        slots.forEach { slot ->
            val isSelected = slot == selected
            val shape = RoundedCornerShape(UsTheme.radii.small)
            Text(
                text = slotTimeText(slot.start),
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = if (isSelected) FontWeight.SemiBold else FontWeight.Normal,
                textAlign = TextAlign.Center,
                textDecoration = if (slot.available) null else TextDecoration.LineThrough,
                color = when {
                    isSelected -> UsTheme.extended.onAccent
                    slot.available -> UsTheme.extended.textPrimary
                    else -> UsTheme.extended.textGhost
                },
                modifier = Modifier
                    .width(SLOT_WIDTH)
                    .clip(shape)
                    .background(if (isSelected) UsTheme.extended.accentSolid else UsTheme.extended.bgCard)
                    .border(1.dp, if (isSelected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle, shape)
                    .clickable(enabled = slot.available) { onSelect(slot) }
                    .padding(vertical = UsTheme.spacing.l),
            )
        }
    }
}

private val DAY_WIDTH = 56.dp
private val SLOT_WIDTH = 100.dp
