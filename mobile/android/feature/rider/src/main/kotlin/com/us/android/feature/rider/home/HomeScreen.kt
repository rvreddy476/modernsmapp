package com.us.android.feature.rider.home

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
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.job.JobActions
import com.us.android.feature.rider.job.JobPhase
import com.us.android.feature.rider.location.RiderLocationService
import com.us.android.feature.rider.money.RiderMoney
import com.us.android.feature.rider.offers.LiveTransport
import com.us.android.feature.rider.offers.OfferCountdown
import com.us.android.feature.rider.offers.OfferSummary
import com.us.android.feature.rider.offers.OfferWindow
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.NavRow
import com.us.android.feature.rider.ui.PartnerStatusText
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderPill
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.SectionHeader
import com.us.android.feature.rider.ui.contentPadding
import com.us.android.feature.rider.ui.explanation
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
fun HomeScreen(
    onOpenOffer: (String) -> Unit,
    onOpenJob: () -> Unit,
    onOpenVerification: () -> Unit,
    onOpenEarnings: () -> Unit,
    onSignOut: () -> Unit,
    viewModel: HomeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current

    val locationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val granted = grants.values.any { it }
        val activity = context.findActivity()
        val canAskAgain = activity != null &&
            ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_FINE_LOCATION)
        viewModel.onPermissionResult(granted, canAskAgain)
    }
    var notificationsGranted by remember { mutableStateOf(context.notificationsGranted()) }
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        notificationsGranted = it
    }

    LaunchedEffect(viewModel) {
        viewModel.events.collect { event ->
            when (event) {
                HomeEvent.RequestLocationPermission -> locationPermission.launch(
                    arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
                )
                HomeEvent.StartLocationService -> RiderLocationService.start(context)
            }
        }
    }

    var now by remember { mutableStateOf(Instant.now()) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(TICK_MILLIS)
            now = Instant.now()
        }
    }

    if (state.duty == DutyState.ShowingDisclosure) {
        LocationDisclosureDialog(
            onContinue = { viewModel.onDisclosureAccepted(context.locationGranted()) },
            onDecline = viewModel::onDisclosureDeclined,
        )
    }

    RiderScreen(
        title = "Feast Rider",
        onBack = null,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        actions = {
            IconButton(onClick = viewModel::refresh) {
                Icon(UsIcons.RotateCw, contentDescription = "Refresh", tint = UsTheme.extended.textMuted)
            }
        },
    ) { padding ->
        if (state.loading) {
            LoadingPane()
            return@RiderScreen
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = contentPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item {
                DutyCard(
                    state = state,
                    onGoOnline = { viewModel.onGoOnlineTapped(context.locationGranted()) },
                    onGoOffline = viewModel::onGoOfflineTapped,
                    onOpenSettings = { context.openAppSettings() },
                )
            }
            if (!notificationsGranted && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                item {
                    RiderCard {
                        CardHeading("Hear new job offers", "Allow notifications so an offer can reach you while the app is in the background.")
                        UsSecondaryButton(
                            text = "Allow notifications",
                            onClick = { notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS) },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
            state.job?.let { job ->
                val actions = JobActions.of(job)
                if (actions.isActive) {
                    item {
                        NavRow(
                            title = "Current job · ${job.orderNumber}",
                            detail = "${job.restaurantName} · ${phaseLabel(actions.phase)}",
                            onClick = onOpenJob,
                            icon = UsIcons.Package,
                        ) {
                            RiderMoney.jobPay(job)?.let { RiderPill(RiderMoney.text(it), PillTone.Positive) }
                        }
                    }
                }
            }
            item {
                SectionHeader(if (state.transport == LiveTransport.LIVE) "Job offers · live" else "Job offers")
            }
            if (state.offers.isEmpty()) {
                item {
                    InfoNote(
                        if (state.duty == DutyState.Online) {
                            "No offers right now. Stay online — new jobs appear here."
                        } else {
                            "Go online to receive delivery jobs near you."
                        },
                    )
                }
            }
            items(state.offers, key = { it.id }) { offer ->
                val window = OfferCountdown.of(offer.expiresAt).at(now)
                if (window != OfferWindow.Expired) {
                    val summary = OfferSummary.of(offer)
                    NavRow(
                        title = listOfNotNull(summary.title, summary.pay).joinToString(" · "),
                        detail = listOfNotNull(
                            summary.dropLocality?.let { "To $it" },
                            summary.tripDistance?.let { "trip $it" },
                            summary.toRestaurant?.let { "pickup $it away" },
                        ).joinToString(" · ").ifBlank { "Distance not given" },
                        onClick = { onOpenOffer(offer.id) },
                        icon = UsIcons.MapPin,
                    ) {
                        if (window is OfferWindow.Open) {
                            RiderPill("${window.remainingSeconds}s", if (window.urgent) PillTone.Danger else PillTone.Accent)
                        }
                    }
                }
            }
            item { SectionHeader("Account") }
            item {
                RiderCard {
                    LabeledValue("Deliveries today", state.deliveriesToday?.toString() ?: "—")
                    LabeledValue("Earned today", RiderMoney.text(state.earningsToday), emphasise = true)
                }
            }
            item { NavRow("Earnings", "Totals and past deliveries", onOpenEarnings, icon = UsIcons.CreditCard) }
            item { NavRow("Verification", "Documents, vehicle and bank account", onOpenVerification, icon = UsIcons.FileText) }
            item { UsSecondaryButton(text = "Sign out", onClick = onSignOut, modifier = Modifier.fillMaxWidth()) }
        }
    }
}

@Composable
private fun DutyCard(state: HomeUiState, onGoOnline: () -> Unit, onGoOffline: () -> Unit, onOpenSettings: () -> Unit) {
    val duty = state.duty
    RiderCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = if (duty == DutyState.Online) "You're online" else "You're offline",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            if (state.partnerStatus.isNotBlank()) {
                RiderPill(PartnerStatusText.label(state.partnerStatus), PartnerStatusText.tone(state.partnerStatus))
            }
        }
        when (duty) {
            DutyState.Online -> {
                InfoNote(
                    when (state.lastPingOk) {
                        false -> "Your location didn't reach Feast just now. Retrying."
                        else -> "Your location is shared with Feast while you're online."
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
                        "Feast Rider needs your location while you're online to offer you jobs."
                    } else {
                        "Location access is off for Feast Rider. Turn it on in Settings to go online."
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
                val allowed = state.partnerStatus.isBlank() || state.partnerStatus in PartnerStatusText.CAN_GO_ONLINE
                if (!allowed) InfoNote("You can go online once Feast approves your account.")
                UsButton(text = "Go online", onClick = onGoOnline, enabled = allowed, modifier = Modifier.fillMaxWidth())
            }
        }
    }
}

/**
 * The prominent disclosure, shown BEFORE the system location prompt (Google
 * Play User Data policy). It says what is collected, when, and why, including
 * that collection continues with the app closed while the rider is online.
 */
@Composable
private fun LocationDisclosureDialog(onContinue: () -> Unit, onDecline: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDecline,
        title = { Text("Feast Rider uses your location") },
        text = {
            Text(
                "While you are online, Feast Rider collects your location — even when the app is closed or not in use — " +
                    "to offer you delivery jobs nearby, to show the restaurant and the customer where you are during a delivery, " +
                    "and to estimate delivery times. Location is not collected while you are offline. " +
                    "A notification stays on screen for as long as your location is being shared.",
            )
        },
        confirmButton = { TextButton(onClick = onContinue) { Text("Continue") } },
        dismissButton = { TextButton(onClick = onDecline) { Text("Not now") } },
    )
}

private fun phaseLabel(phase: JobPhase): String = when (phase) {
    JobPhase.CONFIRM -> "Confirm the job"
    JobPhase.TO_RESTAURANT -> "Head to the restaurant"
    JobPhase.AT_RESTAURANT -> "Waiting for pickup"
    JobPhase.TO_CUSTOMER -> "Head to the customer"
    JobPhase.AT_CUSTOMER -> "At the customer"
    JobPhase.DELIVERED -> "Delivered"
    JobPhase.ENDED -> "Ended"
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

internal fun Context.openAppSettings() {
    startActivity(
        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", packageName, null))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
    )
}

private const val TICK_MILLIS = 1_000L
