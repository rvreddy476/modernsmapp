package com.us.android.feature.dating.travel

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.profile.ProfileOption
import com.us.android.feature.dating.ui.BottomAction
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.SingleOptionChips
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.listPadding
import com.us.android.feature.dating.ui.toneColor
import java.time.ZoneId

/**
 * Travel (mechanic M8): the trip in effect, the cities, how many days, and
 * Start or End. Without a pass the screen is locked and leads to Premium.
 * [onOpenPremium] is where the upsell goes; coming back reads the trip again.
 */
@Composable
fun TravelScreen(onBack: () -> Unit, onOpenPremium: () -> Unit, viewModel: TravelViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // Shown again — back from Premium, where a pass may have landed.
    LaunchedEffect(Unit) { viewModel.refresh() }

    DatingScreen(
        title = TravelCopy.TITLE,
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = {
            if (state.phase == TravelPhase.READY) {
                when {
                    state.locked -> BottomAction(label = TravelCopy.SEE_PREMIUM, onClick = onOpenPremium)
                    else -> BottomAction(
                        label = TravelCopy.START,
                        onClick = viewModel::start,
                        enabled = !state.busy,
                        loading = state.saving,
                        summary = state.city?.let { code ->
                            state.cities.firstOrNull { it.code == code }?.let { "${it.label} · ${TravelCopy.days(state.days)}" }
                        },
                    )
                }
            }
        },
    ) { padding ->
        when (state.phase) {
            TravelPhase.LOADING -> LoadingPane()
            TravelPhase.FAILED -> MessagePane(
                title = TravelCopy.LOAD_FAILED,
                body = "",
                primaryLabel = "Try again",
                onPrimary = viewModel::refresh,
            )
            TravelPhase.OFF -> MessagePane(
                title = TravelCopy.OFF_TITLE,
                body = TravelCopy.OFF_BODY,
                icon = UsIcons.MapPin,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            TravelPhase.READY -> TravelForm(state, viewModel, padding, onOpenPremium)
        }
    }

    if (state.upsell) {
        ConfirmDialog(
            title = TravelCopy.UPSELL_TITLE,
            body = TravelCopy.UPSELL_BODY,
            confirmLabel = TravelCopy.SEE_PREMIUM,
            dismissLabel = TravelCopy.NOT_NOW,
            onConfirm = {
                viewModel.dismissUpsell()
                onOpenPremium()
            },
            onDismiss = viewModel::dismissUpsell,
        )
    }
}

@Composable
private fun TravelForm(state: TravelUiState, viewModel: TravelViewModel, padding: PaddingValues, onOpenPremium: () -> Unit) {
    val enabled = !state.busy && state.available
    LazyColumn(
        contentPadding = listPadding(padding),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        state.trip?.let { trip ->
            item(key = "trip") { ActiveTripCard(trip, ending = state.ending, enabled = !state.busy, onEnd = viewModel::end) }
        }
        if (state.locked) {
            item(key = "locked") { LockedCard(onOpenPremium) }
        } else {
            item(key = "intro") { InfoNote(TravelCopy.INTRO) }
        }
        item(key = "where") { SectionLabel(TravelCopy.WHERE) }
        item(key = "cities") {
            DatingCard {
                SingleOptionChips(
                    options = state.cities.map { ProfileOption(it.code, it.label) },
                    selected = state.city,
                    onSelect = viewModel::selectCity,
                    enabled = enabled,
                )
                FieldError(state.cityError)
            }
        }
        item(key = "how-long") { SectionLabel(TravelCopy.HOW_LONG) }
        item(key = "days") {
            DatingCard {
                DaysStepper(
                    days = state.days,
                    max = state.maxDays,
                    enabled = enabled,
                    onFewer = viewModel::fewerDays,
                    onMore = viewModel::moreDays,
                )
                FieldError(state.daysError)
            }
        }
    }
}

/** The trip in effect, and the way back home. */
@Composable
private fun ActiveTripCard(trip: TripUi, ending: Boolean, enabled: Boolean, onEnd: () -> Unit) {
    val zone = remember { ZoneId.systemDefault() }
    DatingCard {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(20.dp))
            Text(
                TravelCopy.browsingUntil(trip, zone),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(
            "Pulse and your picks show people in ${trip.cityLabel}. Ending the trip brings them back to your own area.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        UsSecondaryButton(text = if (ending) "Ending trip…" else TravelCopy.END, onClick = onEnd, enabled = enabled, modifier = Modifier.fillMaxWidth())
    }
}

@Composable
private fun LockedCard(onOpenPremium: () -> Unit) {
    DatingCard {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            Icon(UsIcons.Lock, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(20.dp))
            Text(
                TravelCopy.LOCKED_TITLE,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
        }
        Text(TravelCopy.LOCKED_BODY, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        UsButton(text = TravelCopy.SEE_PREMIUM, onClick = onOpenPremium, modifier = Modifier.fillMaxWidth())
    }
}

/** 1 to [max] days, one step at a time. */
@Composable
private fun DaysStepper(days: Int, max: Int, enabled: Boolean, onFewer: () -> Unit, onMore: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        StepButton(symbol = "−", label = "One day fewer", enabled = enabled && days > TravelRules.MIN_DAYS, onClick = onFewer)
        Text(
            TravelCopy.days(days),
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
            modifier = Modifier.weight(1f),
        )
        StepButton(symbol = "+", label = "One day more", enabled = enabled && days < max, onClick = onMore)
    }
    Text(
        "Up to ${TravelCopy.days(max)}.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        textAlign = TextAlign.Center,
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun StepButton(symbol: String, label: String, enabled: Boolean, onClick: () -> Unit) {
    val color = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textDim
    Box(
        modifier = Modifier
            .size(44.dp)
            .clip(CircleShape)
            .background(UsTheme.extended.bgRaised, CircleShape)
            .border(1.dp, UsTheme.extended.borderSubtle, CircleShape)
            .clickable(enabled = enabled, onClickLabel = label, role = Role.Button, onClick = onClick)
            .semantics { contentDescription = label },
        contentAlignment = Alignment.Center,
    ) {
        Text(symbol, style = MaterialTheme.typography.titleLarge, color = color)
    }
}

@Composable
private fun FieldError(text: String?) {
    if (text == null) return
    Text(text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.statusDanger)
}

/**
 * Someone else is on a trip: a pin and "Visiting Hyderabad". [onPhoto] draws
 * it over a photo, on the canvas scrim, so it reads on any image.
 */
@Composable
fun VisitingMark(label: String, modifier: Modifier = Modifier, onPhoto: Boolean = false) {
    val color = toneColor(Tone.Accent)
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = modifier
            .background(if (onPhoto) UsTheme.extended.bgCanvas.copy(alpha = SCRIM_ALPHA) else color.copy(alpha = TINT_ALPHA), shape)
            .padding(horizontal = 10.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(UsIcons.MapPin, contentDescription = null, tint = color, modifier = Modifier.size(12.dp))
        Text(
            label,
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.SemiBold,
            color = if (onPhoto) UsTheme.extended.textPrimary else color,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** The deck's line while the viewer is on a trip: "Browsing Mumbai until 9 Oct". Tapping it opens Travel. */
@Composable
fun TripBanner(trip: TripUi, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val zone = remember { ZoneId.systemDefault() }
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val text = TravelCopy.browsingUntil(trip, zone)
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.accentSolid.copy(alpha = TINT_ALPHA), shape)
            .clickable(onClickLabel = TravelCopy.TITLE, role = Role.Button, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(16.dp))
        Text(
            text,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(16.dp))
    }
}

private const val SCRIM_ALPHA = 0.72f
private const val TINT_ALPHA = 0.14f
