package com.us.android.feature.dating.selfie

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.ConsentType
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Tone
import kotlinx.coroutines.delay

/** The selfie step (pending_selfie). [onDone] makes the root re-read the profile. */
@Composable
fun SelfieScreen(
    onBack: () -> Unit,
    onDone: () -> Unit,
    viewModel: SelfieViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    DatingScreen(title = "Verify it's you", onBack = onBack) { padding ->
        Box(Modifier.fillMaxSize().padding(top = padding.calculateTopPadding(), bottom = padding.calculateBottomPadding())) {
            when (val s = state) {
                SelfieState.Loading -> LoadingPane()
                SelfieState.NeedsConsent -> {
                    MessagePane(
                        title = "A quick face check",
                        body = "You'll record a 4-second selfie video and blink twice. We compare it with your main photo.",
                        icon = UsIcons.Camera,
                    )
                    ConfirmDialog(
                        title = ConsentType.BIOMETRIC_SELFIE.title,
                        body = ConsentType.BIOMETRIC_SELFIE.body,
                        confirmLabel = "I agree",
                        dismissLabel = "Not now",
                        onConfirm = { viewModel.onConsentAnswered(granted = true) },
                        onDismiss = { viewModel.onConsentAnswered(granted = false) },
                    )
                }
                SelfieState.ConsentDeclined -> MessagePane(
                    title = "Verification needs your consent",
                    body = "Everyone on Dating is verified with a face check before they can see or message anyone.",
                    icon = UsIcons.Lock,
                    primaryLabel = "Review consent",
                    onPrimary = viewModel::retry,
                )
                is SelfieState.Ready -> RecordStep(s, onRecorded = viewModel::onRecorded, onFailed = viewModel::onRecordingFailed)
                is SelfieState.Uploading -> Column(
                    Modifier.fillMaxSize().padding(UsTheme.spacing.xxl),
                    verticalArrangement = Arrangement.Center,
                ) {
                    Text("Uploading your video…", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                    LinearProgressIndicator(
                        progress = { s.progress },
                        color = UsTheme.extended.accentSolid,
                        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l),
                    )
                }
                SelfieState.Checking -> LoadingPane(label = "Checking…")
                SelfieState.Passed -> MessagePane(
                    title = "You're verified",
                    body = "Thanks. Your profile can now be seen by people nearby.",
                    icon = UsIcons.Check,
                    iconTint = UsTheme.extended.statusSuccess,
                    primaryLabel = "Continue",
                    onPrimary = onDone,
                )
                SelfieState.InReview -> MessagePane(
                    title = "A moderator will take a look",
                    body = "Your video needs a quick human review. We'll let you know when it's done.",
                    icon = UsIcons.Clock,
                    primaryLabel = "OK",
                    onPrimary = onDone,
                )
                is SelfieState.Retry -> MessagePane(
                    title = "Let's try that again",
                    body = s.copy,
                    icon = UsIcons.RotateCcw,
                    iconTint = UsTheme.extended.statusWarning,
                    primaryLabel = "Try again",
                    onPrimary = viewModel::retry,
                    extra = {
                        SelfieOutcomes.attemptsLine(s.attemptsRemaining)?.let {
                            Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textDim, modifier = Modifier.padding(top = UsTheme.spacing.m))
                        }
                    },
                )
                SelfieState.LimitReached -> MessagePane(
                    title = "No attempts left today",
                    body = "You've used all 5 attempts for today. You can try again in 24 hours.",
                    icon = UsIcons.Clock,
                    primaryLabel = "OK",
                    onPrimary = onBack,
                )
                is SelfieState.Blocked -> MessagePane(title = "Not yet", body = s.copy, icon = UsIcons.Info, primaryLabel = "OK", onPrimary = onDone)
                is SelfieState.Error -> MessagePane(
                    title = "That didn't work",
                    body = s.copy,
                    icon = UsIcons.Info,
                    iconTint = UsTheme.extended.statusDanger,
                    primaryLabel = "Try again",
                    onPrimary = viewModel::retry,
                )
            }
        }
    }
}

@Composable
private fun RecordStep(state: SelfieState.Ready, onRecorded: (java.io.File) -> Unit, onFailed: () -> Unit) {
    val context = LocalContext.current
    var permitted by remember {
        mutableStateOf(ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED)
    }
    var denied by remember { mutableStateOf(false) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        permitted = granted
        denied = !granted
    }
    if (!permitted) {
        // The explanation always comes before the system prompt.
        MessagePane(
            title = "Camera for a 4-second video",
            body = if (denied) {
                "Camera access was refused. Allow it in Settings to verify, or try again."
            } else {
                "We record a short front-camera video of you blinking twice. No sound is recorded."
            },
            icon = UsIcons.Camera,
            primaryLabel = "Allow camera",
            onPrimary = { launcher.launch(Manifest.permission.CAMERA) },
        )
        return
    }

    var recorder by remember { mutableStateOf<SelfieRecorder?>(null) }
    var unavailable by remember { mutableStateOf(false) }
    var recording by remember { mutableStateOf(false) }
    var secondsLeft by remember { mutableIntStateOf(0) }

    if (unavailable) {
        MessagePane(title = "No front camera", body = "This device's front camera isn't available, so it can't record the check.", icon = UsIcons.Camera)
        return
    }

    LaunchedEffect(recording) {
        if (!recording) return@LaunchedEffect
        secondsLeft = ((state.recordMillis + MILLIS_PER_SECOND - 1) / MILLIS_PER_SECOND).toInt()
        while (secondsLeft > 0) {
            delay(MILLIS_PER_SECOND)
            secondsLeft -= 1
        }
    }

    Column(
        modifier = Modifier.fillMaxSize().padding(horizontal = UsTheme.spacing.xxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = if (recording) "Blink twice now" else "Look at the camera, then blink twice",
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = UsTheme.spacing.l),
        )
        Box(
            modifier = Modifier
                .fillMaxWidth(PREVIEW_WIDTH)
                .aspectRatio(PREVIEW_RATIO)
                .clip(RoundedCornerShape(UsTheme.radii.card))
                .background(UsTheme.extended.bgRaised),
            contentAlignment = Alignment.BottomCenter,
        ) {
            SelfieCameraPreview(
                onReady = { recorder = it },
                onUnavailable = { unavailable = true },
                modifier = Modifier.fillMaxSize(),
            )
            if (recording) {
                Text(
                    text = "Recording · $secondsLeft",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.statusDanger,
                    modifier = Modifier.padding(12.dp).background(UsTheme.extended.bgCanvas, RoundedCornerShape(UsTheme.radii.full)).padding(horizontal = 12.dp, vertical = 6.dp),
                )
            }
        }
        state.note?.let { InfoNote(it, tone = Tone.Warning) }
        InfoNote("Good light, your whole face in the frame, and only you.")
        UsButton(
            text = if (recording) "Recording…" else "Record 4 seconds",
            enabled = recorder != null && !recording,
            modifier = Modifier.fillMaxWidth(),
            onClick = {
                val r = recorder ?: return@UsButton
                recording = true
                r.record(state.recordMillis) { file ->
                    recording = false
                    if (file != null) onRecorded(file) else onFailed()
                }
            },
        )
    }
}

private const val MILLIS_PER_SECOND = 1_000L
private const val PREVIEW_WIDTH = 0.8f
private const val PREVIEW_RATIO = 0.75f
