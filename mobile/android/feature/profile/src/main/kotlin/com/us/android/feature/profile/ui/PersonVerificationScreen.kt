package com.us.android.feature.profile.ui

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Request verification › Verify you're a real person (2026-11-15). The words match the
 * web's page word for word; every step is the server's `next`.
 */
@Composable
fun PersonVerificationScreen(
    onBack: () -> Unit,
    /** "Apply for the Official mark" on the done state. */
    onOpenOfficialMark: () -> Unit,
    viewModel: PersonVerificationViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(viewModel) {
        viewModel.open.collect { url -> if (!context.openDigiLocker(url)) viewModel.onBrowserMissing() }
    }
    LifecycleResumeEffect(viewModel) {
        viewModel.refreshIfDone()
        onPauseOrDispose {}
    }
    UsScaffold(
        topBar = { UsTopBar(title = "Verify you're a real person", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
                .testTag("person_verification_screen"),
        ) {
            Text(
                text = "We check your Aadhaar through DigiLocker, then take a quick selfie to match it. " +
                    "Your display name must match the name on your Aadhaar.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            val step = state.step
            when {
                state.loading && step == null -> Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                step == null -> {
                    PersonErrorLine(state.loadError ?: "Something went wrong. Try again.")
                    UsSecondaryButton(text = "Try again", onClick = viewModel::load, modifier = Modifier.fillMaxWidth())
                }
                step == PersonStep.DigiLocker -> DigiLockerStep(state, viewModel::start)
                step == PersonStep.Selfie -> SelfieStep(state, viewModel)
                else -> DoneStep(offerOfficialMark = !state.holdsHigherMark, onOpenOfficialMark = onOpenOfficialMark)
            }
        }
    }
}

@Composable
private fun DigiLockerStep(state: PersonVerificationUiState, onStart: () -> Unit) {
    if (state.showRefused) {
        Text(
            "Your last attempt didn't pass. You can try again.",
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.testTag("person_refused"),
        )
    }
    UsButton(
        text = "Continue with DigiLocker",
        onClick = onStart,
        loading = state.starting,
        modifier = Modifier.fillMaxWidth().testTag("person_digilocker"),
    )
    Text(
        "You'll sign in to DigiLocker and come back here.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
    )
    state.error?.let { PersonErrorLine(it) }
}

@Composable
private fun SelfieStep(state: PersonVerificationUiState, viewModel: PersonVerificationViewModel) {
    val context = LocalContext.current
    var permitted by remember { mutableStateOf(context.hasCamera()) }
    var wantRecord by remember { mutableStateOf(false) }
    var recorder by remember { mutableStateOf<PersonSelfieRecorder?>(null) }
    var recording by remember { mutableStateOf(false) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        permitted = granted
        wantRecord = granted
        if (!granted) viewModel.onCameraProblem(CAMERA_REFUSED)
    }
    val record: (PersonSelfieRecorder) -> Unit = { ready ->
        wantRecord = false
        recording = true
        ready.record(SELFIE_MILLIS) { file ->
            recording = false
            viewModel.onSelfieRecorded(file)
        }
    }
    LaunchedEffect(recorder, wantRecord) {
        val ready = recorder
        if (ready != null && wantRecord) record(ready)
    }

    Text(
        "Now a quick selfie. Look at the camera and keep your face in the frame.",
        style = MaterialTheme.typography.bodyMedium,
    )
    if (permitted && !state.checking) {
        PersonSelfiePreview(
            onReady = { recorder = it },
            onUnavailable = {
                recorder = null
                viewModel.onCameraProblem(NO_FRONT_CAMERA)
            },
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(PREVIEW_ASPECT)
                .clip(RoundedCornerShape(UsTheme.radii.large))
                .testTag("person_selfie_preview"),
        )
    }
    UsButton(
        text = if (state.checking) "Checking…" else "Take selfie",
        onClick = {
            val ready = recorder
            when {
                !permitted -> launcher.launch(Manifest.permission.CAMERA)
                ready != null -> record(ready)
                else -> wantRecord = true
            }
        },
        enabled = !state.checking && !recording,
        loading = recording,
        modifier = Modifier.fillMaxWidth().testTag("person_take_selfie"),
    )
    state.error?.let { PersonErrorLine(it) }
}

@Composable
private fun DoneStep(offerOfficialMark: Boolean, onOpenOfficialMark: () -> Unit) {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("person_done")) {
        Text("You're verified", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        Text(
            "Your account is verified as a real person.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
    }
    if (offerOfficialMark) {
        UsButton(
            text = "Apply for the Official mark",
            onClick = onOpenOfficialMark,
            modifier = Modifier.fillMaxWidth().testTag("person_apply_official"),
        )
    }
}

@Composable
private fun PersonErrorLine(message: String) {
    Text(
        text = message,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.statusDanger,
        modifier = Modifier.testTag("person_error"),
    )
}

private fun Context.hasCamera(): Boolean =
    ContextCompat.checkSelfPermission(this, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

/**
 * Opens DigiLocker's sign-in page in the phone's browser. A plain ACTION_VIEW, the way the
 * app hands off every other link: androidx.browser (Custom Tabs) is declared in the version
 * catalogue but is not in the offline build cache yet. DigiLocker returns through the
 * verified App Link, which [com.us.android.feature.profile.data.PersonVerificationReturns] claims.
 */
private fun Context.openDigiLocker(url: String): Boolean {
    val intent = Intent(Intent.ACTION_VIEW, Uri.parse(url))
        .addCategory(Intent.CATEGORY_BROWSABLE)
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    return try {
        startActivity(intent)
        true
    } catch (_: ActivityNotFoundException) {
        false
    } catch (_: SecurityException) {
        false
    }
}

private const val SELFIE_MILLIS = 3_000L
private const val PREVIEW_ASPECT = 3f / 4f
private const val CAMERA_REFUSED = "Camera access was refused. Allow it in Settings to take the selfie."
private const val NO_FRONT_CAMERA = "This phone's front camera isn't available, so it can't take the selfie."
