package com.us.android.feature.mopedu.captain

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.RideBooking
import com.us.android.feature.mopedu.captain.home.DutyState
import com.us.android.feature.mopedu.captain.location.CaptainLocationService
import com.us.android.feature.mopedu.captain.payment.CaptainPaymentRequest
import com.us.android.feature.mopedu.captain.ui.CaptainCard
import com.us.android.feature.mopedu.captain.ui.CaptainDivider
import com.us.android.feature.mopedu.captain.ui.CaptainPill
import com.us.android.feature.mopedu.captain.ui.CaptainScreen
import com.us.android.feature.mopedu.captain.ui.CardHeading
import com.us.android.feature.mopedu.captain.ui.Eyebrow
import com.us.android.feature.mopedu.captain.ui.InfoNote
import com.us.android.feature.mopedu.captain.ui.LabeledValue
import com.us.android.feature.mopedu.captain.ui.LoadingPane
import com.us.android.feature.mopedu.captain.ui.PillTone
import com.us.android.feature.mopedu.captain.ui.SectionHeader
import com.us.android.feature.mopedu.captain.ui.StopRow
import java.util.Locale

/**
 * The captain's console. [onSignOut] is `:app-captain`'s edge; [onOpenPayment]
 * and [onAbandonPayment] are its Activity edges — the plan's payment sheet
 * opens from CaptainActivity, stamped "mopedu".
 */
@Composable
fun MopeduCaptainRoute(
    onSignOut: () -> Unit,
    onOpenPayment: (CaptainPaymentRequest) -> Unit,
    onAbandonPayment: (CaptainPaymentRequest) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: MopeduCaptainViewModel = hiltViewModel(),
) {
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()
    val context = LocalContext.current

    val locationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val granted = grants.values.any { it }
        val activity = context.findActivity()
        val canAskAgain = activity != null &&
            ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_FINE_LOCATION)
        viewModel.onPermissionResult(granted, canAskAgain)
    }
    LaunchedEffect(viewModel) {
        viewModel.events.collect { event ->
            when (event) {
                CaptainEvent.RequestLocationPermission -> locationPermission.launch(
                    arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
                )
                CaptainEvent.StartLocationService -> CaptainLocationService.start(context)
                is CaptainEvent.OpenDigiLocker -> context.openUrl(event.url)
            }
        }
    }

    // The sheet opens from the Activity; the request is handed over exactly once per phase.
    val opening = (uiState as? CaptainUiState.Plans)?.phase as? PlanPhase.OpeningSheet
    LaunchedEffect(opening) { opening?.let { onOpenPayment(it.request) } }
    DisposableEffect(Unit) {
        onDispose {
            val attempt = viewModel.activeAttempt()
            val current = (viewModel.uiState.value as? CaptainUiState.Plans)?.phase as? PlanPhase.OpeningSheet
            if (attempt != null && current != null && current.request.attempt == attempt) onAbandonPayment(current.request)
        }
    }

    when (val state = uiState) {
        CaptainUiState.Loading -> CaptainScreen(title = "Mopedu Captain", onBack = null) { padding -> LoadingPane(Modifier.padding(padding)) }
        is CaptainUiState.Onboarding -> MopeduCaptainOnboardingScreen(
            state = state,
            onBack = null,
            onSubmitProfile = viewModel::submitProfile,
            onSubmitVehicle = viewModel::submitVehicle,
            onSubmitDocument = viewModel::submitDocument,
            onSubmitSelfie = viewModel::submitSelfie,
            onStartDigiLocker = viewModel::startDigiLocker,
            onSubmitForVerification = viewModel::submitForVerification,
            onBackToDocuments = { viewModel.openOnboarding(OnboardingStep.DOCUMENTS) },
            onRefreshStatus = viewModel::refreshOnboardingStatus,
            onProceedToConsole = viewModel::proceedToConsole,
            onDismissError = viewModel::dismissError,
            modifier = modifier,
        )
        is CaptainUiState.Plans -> MopeduCaptainPlansScreen(
            state = state,
            onBack = viewModel::dismissPlans,
            onStartTrial = viewModel::startTrial,
            onChoosePlan = viewModel::choosePlan,
            onCheckAgain = viewModel::checkPaymentAgain,
            onDismissError = viewModel::dismissError,
            modifier = modifier,
        )
        is CaptainUiState.Home -> HomeScreen(
            state = state,
            onGoOnline = { viewModel.onGoOnlineTapped(context.locationGranted()) },
            onGoOffline = viewModel::onGoOfflineTapped,
            onDisclosureAccepted = { viewModel.onDisclosureAccepted(context.locationGranted()) },
            onDisclosureDeclined = viewModel::onDisclosureDeclined,
            onOpenSettings = { context.openAppSettings() },
            onAcceptOffer = viewModel::acceptOffer,
            onRejectOffer = viewModel::rejectOffer,
            onOpenOnboarding = { viewModel.openOnboarding(OnboardingStep.STATUS) },
            onRenew = viewModel::renewPlan,
            onSignOut = onSignOut,
            onDismissError = viewModel::dismissError,
            modifier = modifier,
        )
        is CaptainUiState.EnRouteToPickup -> RideScreen(state.booking, state.errorMessage, viewModel::dismissError, modifier) {
            Eyebrow("En route to pickup", tone = PillTone.Warning)
            CardHeading(state.booking.pickup.address.ifBlank { "Head to the pickup" })
            UsButton(text = "I've arrived at the pickup", onClick = viewModel::markArrived, loading = state.isArriving, modifier = Modifier.fillMaxWidth())
        }
        is CaptainUiState.VerifyOtp -> RideScreen(state.booking, state.errorMessage, viewModel::dismissError, modifier) {
            Eyebrow("At the pickup", tone = PillTone.Positive)
            CardHeading("Ask the customer for their 4-digit start code")
            UsOtpField(value = state.otpInput, onValueChange = viewModel::onOtpInputChanged, length = 4, errorText = state.errorMessage)
            UsButton(text = "Start trip", onClick = viewModel::verifyOtpAndStart, loading = state.isVerifying, modifier = Modifier.fillMaxWidth())
        }
        is CaptainUiState.TripInProgress -> RideScreen(state.booking, state.errorMessage, viewModel::dismissError, modifier) {
            Eyebrow("Trip in progress", tone = PillTone.Positive)
            CardHeading("To ${state.booking.drop.address.ifBlank { "the destination" }}")
            LabeledValue("Fare", (state.booking.finalFare ?: state.booking.estimatedFare).formattedINR)
            LabeledValue("Customer pays by", state.booking.paymentMethod.displayName)
            UsButton(text = "End trip at destination", onClick = { viewModel.completeTrip() }, loading = state.isCompleting, modifier = Modifier.fillMaxWidth())
        }
        is CaptainUiState.CollectPayment -> RideScreen(state.booking, state.errorMessage, viewModel::dismissError, modifier) {
            CollectPaymentContent(state.phase, onConfirmCash = viewModel::confirmCashPayment, onFinish = viewModel::finishRide)
        }
    }
}

// ── Home ────────────────────────────────────────────────────────────────

/** Within three days of the plan running out: a banner above the duty card. */
@Composable
private fun ExpiringPlanBanner(daysLeft: Long, onRenew: () -> Unit, modifier: Modifier = Modifier) {
    CaptainCard(modifier = modifier, onClick = onRenew) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(
                title = if (daysLeft <= 1L) "Your plan runs out today" else "Your plan runs out in $daysLeft days",
                body = "Renew now to keep receiving rides without a break.",
                modifier = Modifier.weight(1f),
                tone = PillTone.Warning,
            )
            CaptainPill("Renew", PillTone.Warning)
        }
    }
}

/** The plan has run out: Home is blocked, and this card is the only way forward. */
@Composable
private fun ExpiredPlanCard(planName: String?, onRenew: () -> Unit, modifier: Modifier = Modifier) {
    CaptainCard(modifier = modifier, highlighted = true) {
        CardHeading(
            title = "Your plan has run out",
            body = "${planName?.ifBlank { null } ?: "Your plan"} has ended, so you can't go online. Renew it to receive rides again.",
            tone = PillTone.Danger,
        )
        UsButton(text = "Renew plan", onClick = onRenew, modifier = Modifier.fillMaxWidth())
    }
}

/** DigiLocker's page, in the browser. A device without one gets the error banner instead of a crash. */
private fun Context.openUrl(url: String) {
    runCatching { startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
}

@Composable
@Suppress("LongParameterList", "LongMethod")
private fun HomeScreen(
    state: CaptainUiState.Home,
    onGoOnline: () -> Unit,
    onGoOffline: () -> Unit,
    onDisclosureAccepted: () -> Unit,
    onDisclosureDeclined: () -> Unit,
    onOpenSettings: () -> Unit,
    onAcceptOffer: () -> Unit,
    onRejectOffer: () -> Unit,
    onOpenOnboarding: () -> Unit,
    onRenew: () -> Unit,
    onSignOut: () -> Unit,
    onDismissError: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    var notificationsGranted by remember { mutableStateOf(context.notificationsGranted()) }
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { notificationsGranted = it }

    if (state.duty == DutyState.ShowingDisclosure) {
        LocationDisclosureDialog(onContinue = onDisclosureAccepted, onDecline = onDisclosureDeclined)
    }

    CaptainScreen(
        title = "Mopedu Captain",
        onBack = null,
        message = state.errorMessage?.let { UsMessage(it) },
        onDismissMessage = onDismissError,
        modifier = modifier,
    ) { padding ->
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = padding,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            // The plan's life: a banner within three days of running out; a blocking card once it has.
            when (val renewal = state.renewal) {
                RenewalNotice.Expired -> item { ExpiredPlanCard(state.subscription?.planName, onRenew, modifier = Modifier.padding(top = UsTheme.spacing.m)) }
                is RenewalNotice.ExpiringSoon -> item { ExpiringPlanBanner(renewal.daysLeft, onRenew, modifier = Modifier.padding(top = UsTheme.spacing.m)) }
                RenewalNotice.None -> Unit
            }
            if (!state.isBlocked) {
                item {
                    DutyCard(
                        state,
                        onGoOnline,
                        onGoOffline,
                        onOpenSettings,
                        modifier = Modifier.padding(top = if (state.renewal == RenewalNotice.None) UsTheme.spacing.m else 0.dp),
                    )
                }
            }
            state.incomingOffer?.let { offer ->
                item { OfferCard(offer, state.offerSecondsLeft, state.isAccepting, onAcceptOffer, onRejectOffer) }
            }
            if (!notificationsGranted && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                item {
                    CaptainCard {
                        CardHeading("Hear ride offers", "Allow notifications so an offer can reach you while the app is in the background.")
                        UsSecondaryButton(
                            text = "Allow notifications",
                            onClick = { notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS) },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
            item { SectionHeader("Today") }
            item {
                CaptainCard {
                    LabeledValue("Rides today", state.stats.todayRides.toString())
                    LabeledValue("Earned today", state.stats.todayEarnings.formattedINR, emphasise = true)
                    LabeledValue("Rating", String.format(Locale.ENGLISH, "%.1f", state.stats.rating))
                    LabeledValue("Total rides", state.stats.totalRidesCompleted.toString())
                    if (!state.stats.totalEarnings.isZero) LabeledValue("Total earned", state.stats.totalEarnings.formattedINR)
                }
            }
            item { SectionHeader("Account") }
            item {
                CaptainCard(onClick = onOpenOnboarding) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CardHeading(
                            title = state.profile?.fullName?.ifBlank { null } ?: "Your profile",
                            body = "KYC ${state.profile?.kycStatus ?: "pending"} · plan ${state.subscription?.planName ?: "none"}",
                            modifier = Modifier.weight(1f),
                        )
                        Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textDim)
                    }
                }
            }
            item { UsSecondaryButton(text = "Sign out", onClick = onSignOut, enabled = !state.isOnline, modifier = Modifier.fillMaxWidth().padding(bottom = UsTheme.spacing.xxxxl)) }
        }
    }
}

@Composable
private fun DutyCard(state: CaptainUiState.Home, onGoOnline: () -> Unit, onGoOffline: () -> Unit, onOpenSettings: () -> Unit, modifier: Modifier = Modifier) {
    val duty = state.duty
    CaptainCard(modifier = modifier, highlighted = state.isOnline) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = if (state.isOnline) "You're online" else "You're offline",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            CaptainPill(if (state.isOnline) "Online" else "Offline", if (state.isOnline) PillTone.Positive else PillTone.Neutral)
        }
        when (duty) {
            DutyState.Online -> {
                InfoNote(
                    when (state.lastPingOk) {
                        false -> "Your location didn't reach Mopedu just now. Retrying."
                        else -> if (state.incomingOffer == null) "Looking for rides near you. Keep the app open." else "A ride is waiting for your answer."
                    },
                    tone = if (state.lastPingOk == false) PillTone.Warning else PillTone.Positive,
                )
                UsSecondaryButton(text = "Go offline", onClick = onGoOffline, modifier = Modifier.fillMaxWidth())
            }
            DutyState.GoingOnline, DutyState.GoingOffline, DutyState.AwaitingPermission, DutyState.ShowingDisclosure ->
                UsButton(text = "Go online", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
            is DutyState.PermissionDenied -> {
                InfoNote(
                    if (duty.canAskAgain) {
                        "Mopedu Captain needs your location while you're online to offer you rides."
                    } else {
                        "Location access is off for Mopedu Captain. Turn it on in Settings to go online."
                    },
                    tone = PillTone.Warning,
                )
                if (duty.canAskAgain) {
                    UsButton(text = "Go online", onClick = onGoOnline, modifier = Modifier.fillMaxWidth())
                } else {
                    UsButton(text = "Open settings", onClick = onOpenSettings, modifier = Modifier.fillMaxWidth())
                }
            }
            is DutyState.Offline -> {
                duty.reason?.let { InfoNote(it.explanation(), tone = PillTone.Warning) }
                InfoNote("Go online to receive ride offers near you.")
                UsButton(text = "Go online", onClick = onGoOnline, modifier = Modifier.fillMaxWidth())
            }
        }
    }
}

/** The offer, with its countdown. The pill turns to danger under ten seconds. */
@Composable
private fun OfferCard(offer: CaptainOffer, secondsLeft: Long, isAccepting: Boolean, onAccept: () -> Unit, onReject: () -> Unit) {
    CaptainCard(highlighted = true) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f)) {
                Eyebrow("New ride offer", tone = PillTone.Warning)
                Text(
                    text = offer.estimatedEarnings.formattedINR,
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                )
            }
            CaptainPill("${secondsLeft}s", if (secondsLeft <= URGENT_SECONDS) PillTone.Danger else PillTone.Accent)
        }
        StopRow("Pickup", offer.pickup.address, UsTheme.extended.statusSuccess)
        StopRow("Drop", offer.drop.address, UsTheme.extended.statusDanger)
        Text(
            text = "${String.format(Locale.ENGLISH, "%.1f km", offer.distanceKM)} · ${offer.vehicleType.displayName} · pays by ${offer.paymentMethod.displayName}",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsSecondaryButton(text = "Decline", onClick = onReject, enabled = !isAccepting, modifier = Modifier.weight(1f))
            UsButton(text = "Accept", onClick = onAccept, loading = isAccepting, modifier = Modifier.weight(1.5f))
        }
    }
}

// ── The ride ────────────────────────────────────────────────────────────

/** A ride screen: the stops on top, the phase's card below. */
@Composable
private fun RideScreen(
    booking: RideBooking,
    errorMessage: String?,
    onDismissError: () -> Unit,
    modifier: Modifier = Modifier,
    card: @Composable () -> Unit,
) {
    CaptainScreen(
        title = "Ride",
        onBack = null,
        message = errorMessage?.let { UsMessage(it) },
        onDismissMessage = onDismissError,
        modifier = modifier,
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding).padding(top = UsTheme.spacing.m), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            CaptainCard {
                StopRow("Pickup", booking.pickup.address, UsTheme.extended.statusSuccess)
                CaptainDivider()
                StopRow("Drop", booking.drop.address, UsTheme.extended.statusDanger)
            }
            CaptainCard(highlighted = true) { card() }
        }
    }
}

/**
 * Collecting the fare. Cash: the captain's confirmation settles it. UPI/card:
 * "Waiting for customer payment" until the SERVER says paid — nothing here
 * can mark an online payment paid.
 */
@Composable
private fun CollectPaymentContent(phase: CollectPhase, onConfirmCash: () -> Unit, onFinish: () -> Unit) {
    Eyebrow("Trip completed", tone = PillTone.Positive)
    when (phase) {
        is CollectPhase.Cash -> {
            CardHeading(if (phase.confirmed) "Cash received" else "Collect cash from the customer")
            Text(text = phase.amount.formattedINR, style = MaterialTheme.typography.displaySmall, fontWeight = FontWeight.Bold, color = UsTheme.extended.textPrimary)
            if (phase.confirmed) {
                InfoNote("Settled. Back to Home in a moment.", tone = PillTone.Positive)
            } else {
                UsButton(text = "Confirm cash received", onClick = onConfirmCash, loading = phase.isConfirming, modifier = Modifier.fillMaxWidth())
            }
        }
        is CollectPhase.WaitingForCustomer -> {
            CardHeading("Waiting for customer payment", "The customer is paying ${phase.amount.formattedINR} by ${phase.method.displayName} in their app.")
            UsButton(text = "Waiting for customer payment", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
            InfoNote("This updates the moment Mopedu confirms the payment. Don't collect cash unless it fails.")
        }
        is CollectPhase.OnlinePaid -> {
            CardHeading("Paid by ${phase.method.displayName}", tone = PillTone.Positive)
            Text(text = phase.amount.formattedINR, style = MaterialTheme.typography.displaySmall, fontWeight = FontWeight.Bold, color = UsTheme.extended.textPrimary)
            InfoNote("Settled. Back to Home in a moment.", tone = PillTone.Positive)
        }
        is CollectPhase.OnlineFailed -> {
            CardHeading("The customer's ${phase.method.displayName} payment failed", "They can retry, or switch to cash in their app. This card updates when they do.", tone = PillTone.Danger)
            UsSecondaryButton(text = "Back to Home", onClick = onFinish, modifier = Modifier.fillMaxWidth())
        }
        CollectPhase.Refunding -> {
            CardHeading("This fare is being refunded", "Mopedu support has stepped in. Nothing to collect.", tone = PillTone.Warning)
            UsSecondaryButton(text = "Back to Home", onClick = onFinish, modifier = Modifier.fillMaxWidth())
        }
    }
}

// ── Dialogs & helpers ───────────────────────────────────────────────────

/** The prominent disclosure, shown BEFORE the system location prompt (Google Play User Data policy). */
@Composable
private fun LocationDisclosureDialog(onContinue: () -> Unit, onDecline: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDecline,
        title = { Text("Mopedu Captain uses your location") },
        text = {
            Text(
                "While you are online, Mopedu Captain collects your location — even when the app is closed or not in use — " +
                    "to offer you rides nearby and to show the customer where you are during a ride. " +
                    "Location is not collected while you are offline. A notification stays on screen for as long as your location is being shared.",
                textAlign = TextAlign.Start,
            )
        },
        confirmButton = { TextButton(onClick = onContinue) { Text("Continue") } },
        dismissButton = { TextButton(onClick = onDecline) { Text("Not now") } },
    )
}

internal fun Context.findActivity(): Activity? {
    var current: Context? = this
    while (current is ContextWrapper) {
        if (current is Activity) return current
        current = current.baseContext
    }
    return null
}

private fun Context.locationGranted(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED ||
        ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

private fun Context.notificationsGranted(): Boolean =
    Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
        ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

private fun Context.openAppSettings() {
    startActivity(
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", packageName, null)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
    )
}

private const val URGENT_SECONDS = 10L
