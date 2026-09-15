package com.us.android.feature.dating.safety

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.listPadding
import com.us.android.feature.dating.ui.toneColor

/** The safety centre. [shareWith] preselects a live-share recipient (opened from a match). */
@Composable
fun SafetyScreen(
    shareWith: String?,
    onBack: () -> Unit,
    viewModel: SafetyViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var confirmPanic by remember { mutableStateOf(false) }
    var recipient by rememberSaveable { mutableStateOf(shareWith) }
    var minutes by rememberSaveable { mutableIntStateOf(SHARE_MINUTES[2]) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val activity = context.findActivity()
        val canAskAgain = activity != null &&
            ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_FINE_LOCATION)
        viewModel.onPermissionResult(grants.values.any { it }, canAskAgain)
    }

    DatingScreen(title = "Safety", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@DatingScreen
        }
        LazyColumn(contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            item {
                DatingCard {
                    Text("Need help?", style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
                    val panic = state.panic
                    if (panic == null) {
                        Text(
                            "This alerts our safety team straight away. If you've allowed location, they'll see where you are.",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textMuted,
                        )
                        UsButton(text = "Alert the safety team", loading = state.panicBusy, onClick = { confirmPanic = true }, modifier = Modifier.fillMaxWidth())
                    } else {
                        Text(
                            if (panic.repeated) "We already have your alert. Our safety team is on it." else "Our safety team has been alerted.",
                            style = MaterialTheme.typography.titleMedium,
                            color = toneColor(Tone.Positive),
                        )
                    }
                    InfoNote("If you're in immediate danger, call 112.", tone = Tone.Danger)
                }
            }

            item { SectionLabel("Trusted contacts (${state.contacts.size} of ${state.maxContacts})") }
            item {
                DatingCard {
                    if (state.contacts.isEmpty()) {
                        Text("Add up to 3 people you trust. They must be a match or one of your connections.", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    }
                    state.contacts.forEach { contact ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text(contact.name, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary, modifier = Modifier.weight(1f))
                            TextButton(onClick = { viewModel.removeContact(contact.userId) }) { Text("Remove", color = UsTheme.extended.textMuted) }
                        }
                    }
                    if (state.contacts.size < state.maxContacts) {
                        if (state.candidates.isEmpty()) {
                            InfoNote("When you have matches, you can add one here.")
                        }
                        state.candidates.forEach { candidate ->
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text(candidate.name, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textSecondary, modifier = Modifier.weight(1f))
                                TextButton(onClick = { viewModel.addContact(candidate.userId) }) { Text("Add", color = UsTheme.extended.accentSolid) }
                            }
                        }
                    }
                }
            }

            item { SectionLabel("Share your live location") }
            item {
                DatingCard {
                    if (state.recipients.isEmpty()) {
                        InfoNote("You can share your location with a match or a trusted contact.")
                    } else {
                        UsChoiceRow(
                            options = state.recipients.map { UsChoice(it.userId, it.name) },
                            selected = recipient,
                            onSelect = { recipient = it },
                            label = "With",
                        )
                        UsChoiceRow(
                            options = SHARE_MINUTES.map { UsChoice(it, if (it < 60) "$it min" else "${it / 60} h") },
                            selected = minutes,
                            onSelect = { it?.let { m -> minutes = m } },
                            label = "For",
                            allowDeselect = false,
                        )
                        UsSecondaryButton(
                            text = if (state.sharing) "Sharing…" else "Share my location",
                            enabled = recipient != null && !state.sharing,
                            modifier = Modifier.fillMaxWidth(),
                            onClick = { recipient?.let { viewModel.share(it, minutes) } },
                        )
                        if (state.location is LocationStep.Denied) {
                            InfoNote("Location isn't allowed, so it can't be shared. You can allow it in Settings.", tone = Tone.Warning)
                        }
                    }
                    state.shares.forEach { share ->
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text("Sharing with ${share.recipientName}", style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary, modifier = Modifier.weight(1f))
                            TextButton(onClick = { viewModel.stopShare(share.shareId) }) { Text("Stop", color = UsTheme.extended.statusDanger) }
                        }
                    }
                }
            }
        }
    }

    if (confirmPanic) {
        ConfirmDialog(
            title = "Alert the safety team?",
            body = "Our team will be told you need help right now.",
            confirmLabel = "Send alert",
            destructive = true,
            onConfirm = {
                confirmPanic = false
                viewModel.panic()
            },
            onDismiss = { confirmPanic = false },
        )
    }
    if (state.location == LocationStep.ExplainingPermission) {
        ConfirmDialog(
            title = "Share your location?",
            body = "Only the person you choose sees it, and only until the time runs out or you stop it.",
            confirmLabel = "Continue",
            dismissLabel = "Not now",
            onConfirm = {
                if (viewModel.onRationaleAccepted() == LocationEffect.RequestPermission) {
                    launcher.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
                }
            },
            onDismiss = viewModel::onRationaleDismissed,
        )
    }
}

/** A location someone is sharing with me. Opened in the maps app; no coordinates are printed. */
@Composable
fun SharedLocationScreen(onBack: () -> Unit, viewModel: SharedLocationViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    DatingScreen(title = "Shared location", onBack = onBack) { _ ->
        when (val s = state) {
            SharedLocationState.Loading -> LoadingPane()
            is SharedLocationState.Ended -> MessagePane(title = s.message, body = "", icon = UsIcons.MapPin, primaryLabel = "Back", onPrimary = onBack)
            is SharedLocationState.Live -> MessagePane(
                title = "Live location shared with you",
                body = "It stays available until the person stops sharing or the time runs out.",
                icon = UsIcons.MapPin,
                primaryLabel = "Open in maps",
                onPrimary = {
                    val lat = s.share.latitude ?: return@MessagePane
                    val lng = s.share.longitude ?: return@MessagePane
                    val uri = Uri.parse("geo:$lat,$lng?q=$lat,$lng")
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, uri)) }
                },
                secondaryLabel = "Refresh",
                onSecondary = viewModel::refresh,
            )
        }
    }
}

private fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}
