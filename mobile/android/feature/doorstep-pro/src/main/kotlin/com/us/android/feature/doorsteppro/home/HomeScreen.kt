package com.us.android.feature.doorsteppro.home

import android.Manifest
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
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
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.domain.JobStatus
import com.us.android.feature.doorsteppro.domain.OfferCountdown
import com.us.android.feature.doorsteppro.domain.OfferWindow
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.location.DutyState
import com.us.android.feature.doorsteppro.location.ProLocationService
import com.us.android.feature.doorsteppro.model.Paise
import com.us.android.feature.doorsteppro.model.toRupeeText
import com.us.android.feature.doorsteppro.realtime.LiveTransport
import com.us.android.feature.doorsteppro.ui.ActionRow
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.SectionLabel
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.countdownText
import com.us.android.feature.doorsteppro.ui.distanceText
import com.us.android.feature.doorsteppro.ui.findActivity
import com.us.android.feature.doorsteppro.ui.humanise
import com.us.android.feature.doorsteppro.ui.label
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.locationGranted
import com.us.android.feature.doorsteppro.ui.notificationsGranted
import com.us.android.feature.doorsteppro.ui.openAppSettings
import com.us.android.feature.doorsteppro.ui.slotRangeText
import com.us.android.feature.doorsteppro.ui.tone
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
@Suppress("LongMethod", "LongParameterList")
fun HomeScreen(
    onOpenOffer: (String) -> Unit,
    onOpenJob: (String) -> Unit,
    onOpenJobs: () -> Unit,
    onOpenChecklist: () -> Unit,
    onOpenEarnings: () -> Unit,
    onOpenAccount: () -> Unit,
    viewModel: HomeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current

    val locationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val activity = context.findActivity()
        val canAskAgain = activity != null && ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_FINE_LOCATION)
        viewModel.onPermissionResult(grants.values.any { it }, canAskAgain)
    }
    var notificationsGranted by remember { mutableStateOf(context.notificationsGranted()) }
    val notificationPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { notificationsGranted = it }

    LaunchedEffect(viewModel) {
        viewModel.events.collect { event ->
            when (event) {
                HomeEvent.RequestLocationPermission -> locationPermission.launch(
                    arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
                )
                HomeEvent.StartLocationService -> ProLocationService.start(context)
            }
        }
    }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        notificationsGranted = context.notificationsGranted()
        viewModel.onResumedWithoutService()
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

    ProScreen(
        title = "Doorstep Pro",
        onBack = null,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        actions = {
            IconButton(onClick = viewModel::refresh) { Icon(UsIcons.RotateCw, contentDescription = "Refresh", tint = UsTheme.extended.textMuted) }
            IconButton(onClick = onOpenAccount) { Icon(UsIcons.Profile, contentDescription = "Account", tint = UsTheme.extended.textMuted) }
        },
    ) { padding ->
        if (state.professional == null && state.status == ProStatus.UNKNOWN) {
            LoadingPane()
            return@ProScreen
        }
        LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            item {
                DutyCard(
                    state = state,
                    onToggle = { on -> if (on) viewModel.onGoOnDutyTapped(context.locationGranted()) else viewModel.onGoOffDutyTapped() },
                    onOpenSettings = { context.openAppSettings() },
                )
            }
            if (!state.canGoOnDuty) {
                item {
                    ActionRow(
                        icon = UsIcons.ShieldAlert,
                        title = if (state.status == ProStatus.APPROVED) "A step needs attention" else "Finish getting verified",
                        subtitle = state.status.label(),
                        onClick = onOpenChecklist,
                        trailing = { Pill(state.status.label(), state.status.tone()) },
                    )
                }
            }
            if (!notificationsGranted) {
                item {
                    ActionRow(
                        icon = UsIcons.Notifications,
                        title = "Turn on notifications",
                        subtitle = "Offers expire in minutes — notifications make sure you hear them.",
                        onClick = { notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS) },
                    )
                }
            }
            item {
                SectionLabel("Offers") {
                    if (state.transport == LiveTransport.POLLING) Pill("Refreshing every 15 s", Tone.Neutral)
                }
            }
            if (state.offers.isEmpty()) {
                item {
                    InfoNote(
                        if (state.duty == DutyState.Online) "No offers right now. New ones appear here and as a notification." else "Go on duty to receive offers.",
                    )
                }
            }
            items(state.offers, key = { "offer-" + it.id }) { offer -> OfferRow(offer, now, onClick = { onOpenOffer(offer.id) }) }
            item { SectionLabel("Active jobs") }
            if (state.activeJobs.isEmpty()) item { InfoNote("No job in progress.") }
            items(state.activeJobs, key = { "job-" + it.bookingId }) { job -> JobRow(job, onClick = { onOpenJob(job.bookingId) }) }
            item { SectionLabel("More") }
            item { ActionRow(icon = UsIcons.Clock, title = "My jobs", subtitle = "Upcoming and past, by date", onClick = onOpenJobs) }
            item { ActionRow(icon = UsIcons.CreditCard, title = "Earnings", subtitle = "What you've earned, job by job", onClick = onOpenEarnings) }
            item { ActionRow(icon = UsIcons.Check, title = "Checklist and documents", subtitle = "Hours, area, skills, certificates", onClick = onOpenChecklist) }
        }
    }
}

@Composable
private fun DutyCard(state: HomeUiState, onToggle: (Boolean) -> Unit, onOpenSettings: () -> Unit) {
    val duty = state.duty
    val on = duty == DutyState.Online || duty == DutyState.GoingOffline
    val busy = duty == DutyState.GoingOnline || duty == DutyState.GoingOffline || duty == DutyState.AwaitingPermission
    ProCard(highlighted = on) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(
                title = if (on) "You're on duty" else "You're off duty",
                subtitle = when {
                    duty == DutyState.GoingOnline -> "Going on duty…"
                    on && state.lastPingOk == false -> "Location isn't reaching Doorstep. Check your connection."
                    on -> "Sharing your location while on duty."
                    else -> state.professional?.displayName?.let { "Hi $it. Go on duty to receive offers." }
                },
                modifier = Modifier.weight(1f),
            )
            Switch(
                checked = on,
                onCheckedChange = onToggle,
                enabled = !busy,
                colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
            )
        }
        if (duty is DutyState.PermissionDenied) {
            InfoNote("Location is needed to go on duty.", tone = Tone.Warning)
            if (!duty.canAskAgain) UsPillButton(text = "Open settings", onClick = onOpenSettings, filled = false)
        }
    }
}

@Composable
private fun OfferRow(offer: OfferDto, now: Instant, onClick: () -> Unit) {
    val window = OfferCountdown.of(offer.expiresAt).at(now)
    ProCard(onClick = onClick, highlighted = true) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(offer.serviceName, "${offer.locality} · ${distanceText(offer.distanceM)}", modifier = Modifier.weight(1f))
            when (window) {
                is OfferWindow.Open -> Pill(countdownText(window.remainingSeconds), if (window.urgent) Tone.Danger else Tone.Warning)
                OfferWindow.Expired -> Pill("Expired", Tone.Neutral)
                OfferWindow.Unknown -> Unit
            }
        }
        Text(slotRangeText(offer.slotStart, offer.slotEnd), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
        Text(
            "Earn about ${Paise(offer.earningEstimatePaise).toRupeeText()}",
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
        )
    }
}

@Composable
internal fun JobRow(job: ProJobDto, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val status = JobStatus.of(job.status)
    ProCard(onClick = onClick, modifier = modifier) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(job.serviceName, "${job.locality} · ${humanise(job.categorySlug)}", modifier = Modifier.weight(1f))
            Pill(status.label, status.tone())
        }
        Text(slotRangeText(job.slotStart, job.slotEnd), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
    }
}

/**
 * The prominent disclosure, shown BEFORE the system location prompt (Google
 * Play User Data policy): what is collected, when, and why, including that
 * collection continues with the app closed while on duty.
 */
@Composable
private fun LocationDisclosureDialog(onContinue: () -> Unit, onDecline: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDecline,
        title = { Text("Doorstep Pro uses your location") },
        text = {
            Text(
                "While you are on duty, Doorstep Pro collects your location — even when the app is closed or not in use — " +
                    "to offer you home-service jobs near you, to show the customer you're on the way, and to confirm you've " +
                    "arrived at their address. Location is not collected while you are off duty. A notification stays on " +
                    "screen for as long as your location is being shared.",
            )
        },
        confirmButton = { TextButton(onClick = onContinue) { Text("Continue") } },
        dismissButton = { TextButton(onClick = onDecline) { Text("Not now") } },
    )
}

private const val TICK_MILLIS = 1_000L
