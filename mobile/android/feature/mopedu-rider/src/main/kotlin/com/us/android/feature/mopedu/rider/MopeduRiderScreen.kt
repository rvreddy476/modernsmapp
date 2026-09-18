package com.us.android.feature.mopedu.rider

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.CaptainInfo
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.feature.mopedu.rider.location.LocationStep
import com.us.android.feature.mopedu.rider.map.RideMapPlaceholder
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import com.us.android.feature.mopedu.rider.ui.CardHeading
import com.us.android.feature.mopedu.rider.ui.Eyebrow
import com.us.android.feature.mopedu.rider.ui.InfoNote
import com.us.android.feature.mopedu.rider.ui.LabeledValue
import com.us.android.feature.mopedu.rider.ui.MessageBanner
import com.us.android.feature.mopedu.rider.ui.MopeduCard
import com.us.android.feature.mopedu.rider.ui.MopeduChoiceRow
import com.us.android.feature.mopedu.rider.ui.MopeduDivider
import com.us.android.feature.mopedu.rider.ui.MopeduPill
import com.us.android.feature.mopedu.rider.ui.PillTone
import com.us.android.feature.mopedu.rider.ui.StopRow
import java.util.Locale

/**
 * The ride screen. [onOpenPayment] and [onAbandonPayment] are `:app`'s
 * Activity edges: the sheet opens from MainActivity, stamped "mopedu".
 */
@Composable
fun MopeduRiderRoute(
    onNavigateBack: () -> Unit,
    onOpenHistory: () -> Unit,
    onOpenPayment: (MopeduPaymentRequest) -> Unit,
    onAbandonPayment: (MopeduPaymentRequest) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: MopeduRiderViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()
    val context = LocalContext.current

    val locationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val granted = grants.values.any { it }
        val activity = context.findActivity()
        val canAskAgain = activity != null &&
            ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_FINE_LOCATION)
        viewModel.onLocationPermissionResult(granted, canAskAgain)
    }
    LaunchedEffect(viewModel) {
        viewModel.events.collect { event ->
            when (event) {
                RiderEvent.RequestLocationPermission -> locationPermission.launch(
                    arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
                )
            }
        }
    }

    // The sheet opens from the Activity; the request is handed over exactly once per phase.
    val opening = (uiState as? RiderUiState.TripCompleted)?.payment as? PaymentPhase.OpeningSheet
    LaunchedEffect(opening) { opening?.let { onOpenPayment(it.request) } }
    DisposableEffect(Unit) {
        onDispose {
            val attempt = viewModel.activeAttempt()
            val current = (viewModel.uiState.value as? RiderUiState.TripCompleted)?.payment as? PaymentPhase.OpeningSheet
            if (attempt != null && current != null && current.request.attempt == attempt) onAbandonPayment(current.request)
        }
    }

    MopeduRiderScreen(
        uiState = uiState,
        onNavigateBack = onNavigateBack,
        onOpenHistory = onOpenHistory,
        onPickupQueryChanged = viewModel::onPickupQueryChanged,
        onDropQueryChanged = viewModel::onDropQueryChanged,
        onUseCurrentLocation = viewModel::useCurrentLocation,
        onLocationRationaleAccepted = viewModel::onLocationRationaleAccepted,
        onLocationRationaleDismissed = viewModel::onLocationRationaleDismissed,
        onRequestQuote = viewModel::requestQuote,
        onBackToLocations = viewModel::backToLocations,
        onSelectOption = viewModel::selectVehicleOption,
        onSelectPaymentMethod = viewModel::selectPaymentMethod,
        onCouponInputChanged = viewModel::onCouponInputChanged,
        onApplyCoupon = viewModel::applyCoupon,
        onRemoveCoupon = viewModel::removeCoupon,
        onConfirmBooking = viewModel::confirmBooking,
        onRequestCancel = viewModel::requestCancel,
        onDismissCancel = viewModel::dismissCancel,
        onConfirmCancel = viewModel::confirmCancel,
        onTriggerSOS = viewModel::triggerSOS,
        onGenerateShare = viewModel::generateShareLink,
        onPayNow = viewModel::payNow,
        onSwitchToCash = viewModel::switchToCash,
        onCheckPaymentAgain = viewModel::checkPaymentAgain,
        onSubmitRating = viewModel::submitRating,
        onReset = viewModel::resetToNewBooking,
        onDismissError = viewModel::dismissError,
        modifier = modifier,
    )
}

@Composable
@Suppress("LongParameterList", "LongMethod")
fun MopeduRiderScreen(
    uiState: RiderUiState,
    onNavigateBack: () -> Unit,
    onOpenHistory: () -> Unit,
    onPickupQueryChanged: (String) -> Unit,
    onDropQueryChanged: (String) -> Unit,
    onUseCurrentLocation: () -> Unit,
    onLocationRationaleAccepted: () -> Unit,
    onLocationRationaleDismissed: () -> Unit,
    onRequestQuote: () -> Unit,
    onBackToLocations: () -> Unit,
    onSelectOption: (QuoteOption) -> Unit,
    onSelectPaymentMethod: (PaymentMethod) -> Unit,
    onCouponInputChanged: (String) -> Unit,
    onApplyCoupon: () -> Unit,
    onRemoveCoupon: () -> Unit,
    onConfirmBooking: () -> Unit,
    onRequestCancel: () -> Unit,
    onDismissCancel: () -> Unit,
    onConfirmCancel: () -> Unit,
    onTriggerSOS: () -> Unit,
    onGenerateShare: () -> Unit,
    onPayNow: () -> Unit,
    onSwitchToCash: () -> Unit,
    onCheckPaymentAgain: () -> Unit,
    onSubmitRating: (Int, String) -> Unit,
    onReset: () -> Unit,
    onDismissError: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val booking = uiState.bookingOrNull()
    val pickup = when (uiState) {
        is RiderUiState.LocationSelect -> uiState.pickup
        is RiderUiState.QuoteSelect -> uiState.quote.pickup
        else -> booking?.pickup
    }
    val drop = when (uiState) {
        is RiderUiState.LocationSelect -> uiState.drop
        is RiderUiState.QuoteSelect -> uiState.quote.drop
        else -> booking?.drop
    }
    val captainLocation = (uiState as? RiderUiState.CaptainAssigned)?.captainLocation
    val error = uiState.errorOrNull()

    if (uiState is RiderUiState.LocationSelect && uiState.locationStep == LocationStep.ExplainingPermission) {
        LocationRationaleDialog(onContinue = onLocationRationaleAccepted, onDismiss = onLocationRationaleDismissed)
    }
    uiState.cancelPromptOrNull()?.let { prompt ->
        CancelDialog(prompt = prompt, onConfirm = onConfirmCancel, onDismiss = onDismissCancel)
    }

    UsScaffold(
        modifier = modifier,
        applyPageGutter = false,
        topBar = {
            UsTopBar(
                title = "Mopedu",
                onBack = onNavigateBack,
                actions = {
                    IconButton(onClick = onOpenHistory) {
                        Icon(UsIcons.Clock, contentDescription = "Ride history", tint = UsTheme.extended.textMuted)
                    }
                },
            )
        },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            RideMapPlaceholder(pickup = pickup, drop = drop, captainLocation = captainLocation, modifier = Modifier.fillMaxSize())

            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .align(Alignment.BottomCenter)
                    .verticalScroll(rememberScrollState())
                    .padding(UsTheme.spacing.xxl),
            ) {
                when (uiState) {
                    is RiderUiState.LocationSelect -> LocationSearchCard(
                        state = uiState,
                        onPickupQueryChanged = onPickupQueryChanged,
                        onDropQueryChanged = onDropQueryChanged,
                        onUseCurrentLocation = onUseCurrentLocation,
                        onRequestQuote = onRequestQuote,
                    )
                    is RiderUiState.QuoteSelect -> QuoteSelectionCard(
                        state = uiState,
                        onSelectOption = onSelectOption,
                        onSelectPaymentMethod = onSelectPaymentMethod,
                        onCouponInputChanged = onCouponInputChanged,
                        onApplyCoupon = onApplyCoupon,
                        onRemoveCoupon = onRemoveCoupon,
                        onConfirmBooking = onConfirmBooking,
                        onBack = onBackToLocations,
                    )
                    is RiderUiState.SearchingCaptain -> SearchingCaptainCard(onCancel = onRequestCancel)
                    is RiderUiState.CaptainAssigned -> CaptainAssignedCard(
                        booking = uiState.booking,
                        etaMinutes = uiState.etaMinutes,
                        onCancel = onRequestCancel,
                    )
                    is RiderUiState.ArrivedAtPickup -> ArrivedWithOtpCard(
                        otp = uiState.otp,
                        captain = uiState.booking.captain,
                        onCancel = onRequestCancel,
                    )
                    is RiderUiState.TripInProgress -> TripInProgressCard(
                        booking = uiState.booking,
                        shareLink = uiState.shareLink,
                        sosTriggered = uiState.sosTriggered,
                        onTriggerSOS = onTriggerSOS,
                        onGenerateShare = onGenerateShare,
                    )
                    is RiderUiState.Cancelled -> CancelledCard(state = uiState, onReset = onReset)
                    is RiderUiState.TripCompleted -> TripCompletedCard(
                        receipt = uiState.receipt,
                        payment = uiState.payment,
                        ratingSubmitted = uiState.ratingSubmitted,
                        onPayNow = onPayNow,
                        onSwitchToCash = onSwitchToCash,
                        onCheckPaymentAgain = onCheckPaymentAgain,
                        onSubmitRating = onSubmitRating,
                        onReset = onReset,
                    )
                }
            }

            if (error != null) {
                MessageBanner(
                    message = UsMessage(error),
                    onDismiss = onDismissError,
                    modifier = Modifier
                        .align(Alignment.TopCenter)
                        .padding(UsTheme.spacing.xxl),
                )
            }
        }
    }
}

// ── Cards ───────────────────────────────────────────────────────────────

@Composable
private fun LocationSearchCard(
    state: RiderUiState.LocationSelect,
    onPickupQueryChanged: (String) -> Unit,
    onDropQueryChanged: (String) -> Unit,
    onUseCurrentLocation: () -> Unit,
    onRequestQuote: () -> Unit,
) {
    MopeduCard {
        CardHeading("Where are you going?")
        UsTextField(
            value = state.pickupQuery,
            onValueChange = onPickupQueryChanged,
            label = "Pickup",
            placeholder = "Your pickup address",
            modifier = Modifier.fillMaxWidth(),
        )
        val locating = state.locationStep == LocationStep.Locating || state.locationStep == LocationStep.AwaitingPermission
        UsSecondaryButton(
            text = if (locating) "Finding you…" else "Use my location",
            onClick = onUseCurrentLocation,
            enabled = !locating,
            modifier = Modifier.fillMaxWidth(),
        )
        when (val step = state.locationStep) {
            is LocationStep.Denied -> InfoNote(
                if (step.canAskAgain) {
                    "Allow location to set your pickup automatically, or type it above."
                } else {
                    "Location is off for Momentum. Turn it on in Settings, or type your pickup above."
                },
                tone = PillTone.Warning,
            )
            LocationStep.Unavailable -> InfoNote("We couldn't get a fix. Type your pickup above.", tone = PillTone.Warning)
            else -> Unit
        }
        UsTextField(
            value = state.dropQuery,
            onValueChange = onDropQueryChanged,
            label = "Destination",
            placeholder = "Where to?",
            modifier = Modifier.fillMaxWidth(),
        )
        UsButton(
            text = "View fares",
            onClick = onRequestQuote,
            loading = state.isLoading,
            enabled = state.pickupQuery.isNotBlank() && state.dropQuery.isNotBlank(),
            modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m),
        )
    }
}

@Composable
@Suppress("LongMethod")
private fun QuoteSelectionCard(
    state: RiderUiState.QuoteSelect,
    onSelectOption: (QuoteOption) -> Unit,
    onSelectPaymentMethod: (PaymentMethod) -> Unit,
    onCouponInputChanged: (String) -> Unit,
    onApplyCoupon: () -> Unit,
    onRemoveCoupon: () -> Unit,
    onConfirmBooking: () -> Unit,
    onBack: () -> Unit,
) {
    val selected = state.selectedOption
    MopeduCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading("Choose your ride", modifier = Modifier.weight(1f))
            TextButton(onClick = onBack) { Text("Change", color = UsTheme.extended.accentSolid) }
        }
        StopRow("Pickup", state.quote.pickup.address, UsTheme.extended.statusSuccess)
        StopRow("Destination", state.quote.drop.address, UsTheme.extended.statusDanger)
        MopeduDivider()

        state.quote.options.forEach { option ->
            MopeduChoiceRow(selected = option.vehicleType == selected.vehicleType, onClick = { onSelectOption(option) }) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(modifier = Modifier.weight(1f)) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                            Text(
                                text = option.vehicleType.displayName,
                                style = MaterialTheme.typography.bodyLarge,
                                fontWeight = FontWeight.SemiBold,
                                color = UsTheme.extended.textPrimary,
                            )
                            // The chip is drawn ONLY for a named surge reason; `none` never renders it.
                            option.surgeReason.chipLabel?.takeIf { option.showsSurgeChip }?.let { label ->
                                MopeduPill(label, PillTone.Warning)
                            }
                        }
                        Text(
                            text = "${option.pickupETASeconds / SECONDS_PER_MINUTE} min away · ${formatKm(option.distanceMeters)}" +
                                if (!option.available) " · unavailable" else "",
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                    Text(
                        text = option.totalFare.formattedINR,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.textPrimary,
                    )
                }
            }
            Spacer(Modifier.height(UsTheme.spacing.xs))
        }

        FareBreakdown(option = selected)

        MopeduDivider()
        CouponField(state = state, onCouponInputChanged = onCouponInputChanged, onApply = onApplyCoupon, onRemove = onRemoveCoupon)

        MopeduDivider()
        Eyebrow("Pay with", tone = PillTone.Neutral)
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            PaymentMethod.entries.forEach { method ->
                MopeduChoiceRow(
                    selected = state.paymentMethod == method,
                    onClick = { onSelectPaymentMethod(method) },
                    modifier = Modifier.weight(1f),
                ) {
                    Text(
                        text = method.displayName,
                        style = MaterialTheme.typography.labelLarge,
                        color = UsTheme.extended.textPrimary,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        }
        if (state.paymentMethod.isOnline) {
            InfoNote("You'll pay ${state.paymentMethod.displayName} in the app once the trip ends.")
        } else {
            InfoNote("Pay your captain in cash at the end of the trip.")
        }

        UsButton(
            text = "Book ${selected.vehicleType.displayName} · ${selected.totalFare.formattedINR}",
            onClick = onConfirmBooking,
            loading = state.isBooking,
            enabled = selected.available && !state.isRepricing,
            modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m),
        )
    }
}

/** The server's lines for the selected option. Only lines with money on them are shown. */
@Composable
private fun FareBreakdown(option: QuoteOption) {
    val b = option.breakdown
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Eyebrow(
            text = "Fare" + (option.windowName?.takeIf { it.isNotBlank() }?.let { " · $it" } ?: ""),
            tone = PillTone.Neutral,
        )
        breakdownLine("Base fare", b.basePaise)
        breakdownLine("Distance", b.distancePaise)
        breakdownLine("Time", b.timePaise)
        breakdownLine("Waiting", b.waitingPaise)
        if (b.surgePaise > 0) {
            LabeledValue(
                label = "Surge" + option.surgeReason.chipLabel?.let { " · $it" }.orEmpty() +
                    if (option.surgeBasisPoints > 0) " (+${option.surgeBasisPoints / BASIS_POINTS_PER_PERCENT}%)" else "",
                value = MoneyPaise(b.surgePaise).formattedINR,
                tone = PillTone.Warning,
            )
        }
        breakdownLine("Platform fee", b.platformFeePaise)
        breakdownLine("Tax", b.taxPaise)
        if (option.hasDiscount) {
            LabeledValue(
                label = "Discount" + option.couponCode?.let { " · $it" }.orEmpty(),
                value = "−${option.discount.formattedINR}",
                tone = PillTone.Positive,
            )
        }
        if (option.includesOutstanding) {
            LabeledValue("Previous cancellation fee", option.outstanding.formattedINR, tone = PillTone.Warning)
            InfoNote("Includes previous cancellation fee ${option.outstanding.formattedINR}", tone = PillTone.Warning)
        }
        LabeledValue("Total", option.totalFare.formattedINR, emphasise = true)
        option.taxNote?.takeIf { it.isNotBlank() }?.let { InfoNote(it) }
    }
}

@Composable
private fun breakdownLine(label: String, paise: Long) {
    if (paise > 0) LabeledValue(label, MoneyPaise(paise).formattedINR)
}

@Composable
private fun CouponField(
    state: RiderUiState.QuoteSelect,
    onCouponInputChanged: (String) -> Unit,
    onApply: () -> Unit,
    onRemove: () -> Unit,
) {
    when (val coupon = state.coupon) {
        is CouponStatus.Applied -> {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(modifier = Modifier.weight(1f)) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                        Text(coupon.coupon.code, style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textPrimary)
                        MopeduPill("Applied", PillTone.Positive)
                    }
                    if (coupon.coupon.description.isNotBlank()) {
                        Text(coupon.coupon.description, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                    }
                }
                TextButton(onClick = onRemove, enabled = !state.isRepricing) { Text("Remove", color = UsTheme.extended.accentSolid) }
            }
        }
        else -> {
            Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsTextField(
                    value = state.couponInput,
                    onValueChange = onCouponInputChanged,
                    label = "Coupon",
                    placeholder = "Enter a code",
                    errorText = (coupon as? CouponStatus.Rejected)?.message,
                    enabled = coupon != CouponStatus.Checking,
                    modifier = Modifier.weight(1f),
                )
                UsSecondaryButton(
                    text = if (coupon == CouponStatus.Checking) "Checking…" else "Apply",
                    onClick = onApply,
                    enabled = state.couponInput.isNotBlank() && coupon != CouponStatus.Checking && !state.isRepricing,
                )
            }
        }
    }
}

@Composable
private fun SearchingCaptainCard(onCancel: () -> Unit) {
    MopeduCard {
        Column(modifier = Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            CircularProgressIndicator(color = UsTheme.extended.accentSolid, modifier = Modifier.size(44.dp))
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            Text(
                text = "Finding your captain…",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "Offering your ride to the nearest verified captains.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = UsTheme.spacing.xs),
            )
        }
        UsSecondaryButton(text = "Cancel ride", onClick = onCancel, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m))
    }
}

@Composable
private fun CaptainAssignedCard(booking: RideBooking, etaMinutes: Int, onCancel: () -> Unit) {
    MopeduCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                Eyebrow("Captain on the way", tone = PillTone.Positive)
                Text(
                    text = "Arriving in $etaMinutes min",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
            }
            booking.captain?.vehicleNumber?.takeIf { it.isNotBlank() }?.let { MopeduPill(it, PillTone.Accent) }
        }
        MopeduDivider()
        CaptainRow(booking.captain)
        UsSecondaryButton(text = "Cancel ride", onClick = onCancel, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m))
    }
}

@Composable
private fun CaptainRow(captain: CaptainInfo?) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(UsIcons.Profile, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(28.dp))
        Spacer(Modifier.width(UsTheme.spacing.l))
        Column {
            Text(
                text = captain?.name?.ifBlank { null } ?: "Your captain",
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
            val detail = listOfNotNull(
                captain?.vehicleModel?.takeIf { it.isNotBlank() },
                captain?.rating?.let { String.format(Locale.ENGLISH, "%.1f rating", it) },
            ).joinToString(" · ")
            if (detail.isNotBlank()) Text(detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
    }
}

@Composable
private fun ArrivedWithOtpCard(otp: String, captain: CaptainInfo?, onCancel: () -> Unit) {
    MopeduCard(highlighted = true) {
        Column(modifier = Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            CardHeading("Your captain has arrived", tone = PillTone.Positive)
            Text(
                text = "Tell your captain this start code:",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(top = UsTheme.spacing.xs),
            )
            Text(
                text = otp.ifBlank { "····" },
                style = MaterialTheme.typography.displaySmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.padding(vertical = UsTheme.spacing.l),
            )
            Text(
                text = listOfNotNull(captain?.name?.takeIf { it.isNotBlank() }, captain?.vehicleNumber?.takeIf { it.isNotBlank() })
                    .joinToString(" · ")
                    .ifBlank { "Look for your captain at the pickup" },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        UsSecondaryButton(text = "Cancel ride", onClick = onCancel, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m))
    }
}

@Composable
private fun TripInProgressCard(
    booking: RideBooking,
    shareLink: String?,
    sosTriggered: Boolean,
    onTriggerSOS: () -> Unit,
    onGenerateShare: () -> Unit,
) {
    MopeduCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                Eyebrow("Trip in progress", tone = PillTone.Positive)
                Text(
                    text = "To ${booking.drop.address.ifBlank { "your destination" }}",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
            }
            MopeduPill(if (sosTriggered) "SOS sent" else "SOS", PillTone.Danger, modifier = Modifier.padding(start = UsTheme.spacing.m))
        }
        MopeduDivider()
        LabeledValue("Fare", (booking.finalFare ?: booking.estimatedFare).formattedINR)
        LabeledValue("Paying by", booking.paymentMethod.displayName)
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), modifier = Modifier.padding(top = UsTheme.spacing.m)) {
            UsSecondaryButton(
                text = if (shareLink != null) "Link copied" else "Share trip",
                onClick = onGenerateShare,
                enabled = shareLink == null,
                modifier = Modifier.weight(1f),
            )
            UsButton(text = if (sosTriggered) "SOS active" else "Emergency", onClick = onTriggerSOS, enabled = !sosTriggered, modifier = Modifier.weight(1f))
        }
        shareLink?.let { InfoNote("Live trip link: $it") }
    }
}

@Composable
private fun CancelledCard(state: RiderUiState.Cancelled, onReset: () -> Unit) {
    MopeduCard {
        CardHeading(
            title = if (state.byCustomer) "Ride cancelled" else "Your ride couldn't go ahead",
            body = when {
                !state.byCustomer -> "No captain could take this ride. You have not been charged."
                state.fee.isZero -> "No cancellation fee was charged."
                else -> "A cancellation fee of ${state.fee.formattedINR} applies. It is added to your next ride."
            },
            tone = if (state.byCustomer && !state.fee.isZero) PillTone.Warning else null,
        )
        UsButton(text = "Book another ride", onClick = onReset, modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m))
    }
}

@Composable
@Suppress("LongMethod")
private fun TripCompletedCard(
    receipt: RideReceipt,
    payment: PaymentPhase,
    ratingSubmitted: Boolean,
    onPayNow: () -> Unit,
    onSwitchToCash: () -> Unit,
    onCheckPaymentAgain: () -> Unit,
    onSubmitRating: (Int, String) -> Unit,
    onReset: () -> Unit,
) {
    MopeduCard {
        CardHeading("Trip completed", tone = PillTone.Positive)
        Text(
            text = receipt.totalFare.formattedINR,
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
        if (receipt.lines.isNotEmpty()) {
            receipt.lines.forEach { line -> LabeledValue(line.label, line.amount.formattedINR) }
            receipt.taxNote?.takeIf { it.isNotBlank() }?.let { InfoNote(it) }
        }
        if (receipt.distanceMeters > 0 || receipt.durationSeconds > 0) {
            Text(
                text = "${formatKm(receipt.distanceMeters)} · ${receipt.durationSeconds / SECONDS_PER_MINUTE} min",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        MopeduDivider()
        PaymentSection(payment, onPayNow, onSwitchToCash, onCheckPaymentAgain)
        MopeduDivider()
        if (!ratingSubmitted) {
            Text("How was your ride?", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                (1..STARS).forEach { stars ->
                    IconButton(onClick = { onSubmitRating(stars, "") }) {
                        Icon(UsIcons.HeartOutline, contentDescription = "$stars stars", tint = UsTheme.extended.accentSolid)
                    }
                }
            }
        } else {
            InfoNote("Thanks for your feedback.", tone = PillTone.Positive)
        }
        val settled = payment == PaymentPhase.Paid || payment == PaymentPhase.CashConfirmed || payment == PaymentPhase.Refunding
        UsButton(
            text = "Book another ride",
            onClick = onReset,
            enabled = settled || payment is PaymentPhase.StillConfirming || payment is PaymentPhase.CashPending,
            modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m),
        )
    }
}

/** The money after the trip. Only the server's word moves it to a settled line. */
@Composable
private fun PaymentSection(
    payment: PaymentPhase,
    onPayNow: () -> Unit,
    onSwitchToCash: () -> Unit,
    onCheckPaymentAgain: () -> Unit,
) {
    when (payment) {
        is PaymentPhase.CashPending -> {
            Eyebrow("Pay in cash", tone = PillTone.Warning)
            InfoNote("Pay ${payment.amount.formattedINR} to your captain. This updates once they confirm.", tone = PillTone.Warning)
        }
        PaymentPhase.CashConfirmed -> InfoNote("Cash received by your captain.", tone = PillTone.Positive)
        is PaymentPhase.ReadyToPay -> {
            Eyebrow("Pay ${payment.method.displayName}", tone = PillTone.Accent)
            UsButton(
                text = "Pay ${payment.amount.formattedINR}",
                onClick = onPayNow,
                loading = payment.creatingIntent,
                modifier = Modifier.fillMaxWidth(),
            )
            UsSecondaryButton(text = "Pay cash instead", onClick = onSwitchToCash, modifier = Modifier.fillMaxWidth())
        }
        is PaymentPhase.OpeningSheet -> UsButton(text = "Opening payment…", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
        is PaymentPhase.Confirming -> {
            UsButton(text = "Confirming payment…", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
            InfoNote("Waiting for the bank's confirmation. Don't pay again.")
        }
        PaymentPhase.Paid -> InfoNote("Paid. Thank you.", tone = PillTone.Positive)
        is PaymentPhase.Failed -> {
            InfoNote(payment.reason ?: "Your payment didn't go through. You haven't been charged.", tone = PillTone.Danger)
            if (payment.retryable) {
                UsButton(text = "Try ${payment.method.displayName} again", onClick = onPayNow, modifier = Modifier.fillMaxWidth())
            }
            UsSecondaryButton(text = "Pay cash instead", onClick = onSwitchToCash, modifier = Modifier.fillMaxWidth())
        }
        PaymentPhase.Refunding -> InfoNote("Your payment is being refunded.", tone = PillTone.Warning)
        PaymentPhase.StillConfirming -> {
            InfoNote("We're still waiting for the bank. If you paid, it will show here shortly.", tone = PillTone.Warning)
            UsSecondaryButton(text = "Check again", onClick = onCheckPaymentAgain, modifier = Modifier.fillMaxWidth())
        }
    }
}

// ── Dialogs ─────────────────────────────────────────────────────────────

/** The fee rule the ride carries, stated BEFORE the customer confirms. */
@Composable
private fun CancelDialog(prompt: CancelPrompt, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Cancel this ride?") },
        text = {
            Text(
                when {
                    prompt.isFree && prompt.freeSecondsLeft != null ->
                        "Cancelling now is free. A cancellation fee applies after ${prompt.freeSecondsLeft} more seconds."
                    prompt.isFree -> "Cancelling now is free."
                    else -> "A cancellation fee of ${prompt.fee.formattedINR} will be charged and added to your next ride."
                },
            )
        },
        confirmButton = {
            TextButton(onClick = onConfirm, enabled = !prompt.isCancelling) {
                Text(if (prompt.isFree) "Cancel ride" else "Cancel and pay ${prompt.fee.formattedINR}")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Keep ride") } },
    )
}

@Composable
private fun LocationRationaleDialog(onContinue: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Use your location for pickup") },
        text = {
            Text(
                "Momentum reads your location once, to set your pickup point. It is not tracked and not shared with anyone " +
                    "other than the captain who takes your ride.",
            )
        },
        confirmButton = { TextButton(onClick = onContinue) { Text("Continue") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Not now") } },
    )
}

// ── Helpers ─────────────────────────────────────────────────────────────

private fun RiderUiState.bookingOrNull(): RideBooking? = when (this) {
    is RiderUiState.SearchingCaptain -> booking
    is RiderUiState.CaptainAssigned -> booking
    is RiderUiState.ArrivedAtPickup -> booking
    is RiderUiState.TripInProgress -> booking
    is RiderUiState.Cancelled -> booking
    else -> null
}

private fun RiderUiState.errorOrNull(): String? = when (this) {
    is RiderUiState.LocationSelect -> error
    is RiderUiState.QuoteSelect -> error
    is RiderUiState.SearchingCaptain -> error
    is RiderUiState.CaptainAssigned -> error
    is RiderUiState.ArrivedAtPickup -> error
    is RiderUiState.TripCompleted -> error
    else -> null
}

private fun RiderUiState.cancelPromptOrNull(): CancelPrompt? = when (this) {
    is RiderUiState.SearchingCaptain -> cancel
    is RiderUiState.CaptainAssigned -> cancel
    is RiderUiState.ArrivedAtPickup -> cancel
    else -> null
}

private fun formatKm(meters: Int): String = String.format(Locale.ENGLISH, "%.1f km", meters / METERS_PER_KM)

internal fun Context.findActivity(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}

private const val SECONDS_PER_MINUTE = 60
private const val METERS_PER_KM = 1000.0
private const val BASIS_POINTS_PER_PERCENT = 100
private const val STARS = 5
