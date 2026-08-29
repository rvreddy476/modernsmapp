package com.us.android.feature.call.ui

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledIconToggleButton
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.us.android.core.call.CallState
import kotlinx.coroutines.delay

/**
 * The one call surface: outgoing ring, incoming ring, connecting, active
 * (audio or video) and the ended card. Dark, full-bleed, self-contained —
 * calls do not inherit the app scaffold.
 */
@Composable
fun CallScreen(
    onBack: () -> Unit,
    viewModel: CallViewModel = hiltViewModel(),
) {
    val state by viewModel.callState.collectAsState()
    val message by viewModel.message.collectAsState()
    val permissions = rememberCallPermissionFlow(viewModel, state)

    // The ended card shows briefly, then the surface dismisses itself.
    LaunchedEffect(state) {
        if (state is CallState.Ended) {
            delay(ENDED_CARD_MILLIS)
            viewModel.dismissEnded()
            onBack()
        }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(BACKDROP))
            .testTag("call-screen"),
    ) {
        CallStateContent(
            state = state,
            idleMessage = message,
            viewModel = viewModel,
            onAccept = permissions.onAccept,
            showOpenSettings = permissions.showOpenSettings,
            onOpenSettings = permissions.onOpenSettings,
        )

        message?.let {
            Text(
                text = it,
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .padding(24.dp)
                    .testTag("call-message"),
            )
        }
    }
}

/** What [CallScreen] needs from the permission machinery, per frame. */
private class CallPermissionFlow(
    val showOpenSettings: Boolean,
    val onAccept: () -> Unit,
    val onOpenSettings: () -> Unit,
)

/**
 * CALL-LB-6: the whole permission journey, on REAL Android state.
 *
 * Permission truth is READ FROM ANDROID, never from remembered launcher
 * booleans — those go stale the moment the user grants from Settings or a
 * later request. The ACCEPT boundary requires the microphone plus, for
 * video, the camera; anything missing is requested from that very tap and
 * the acceptance continues exactly once on its grant. A denial keeps the
 * call Incoming (Accept is the retry action) and a permanent camera denial
 * adds an Open Settings action, re-read on resume. An incoming video invite
 * never auto-prompts for the camera — that request belongs to Accept.
 */
@Composable
private fun rememberCallPermissionFlow(
    viewModel: CallViewModel,
    state: CallState,
): CallPermissionFlow {
    val context = LocalContext.current
    var grantsVersion by remember { mutableIntStateOf(0) }
    val cameraGranted = remember(grantsVersion) {
        context.hasPermission(Manifest.permission.CAMERA)
    }
    var acceptPending by remember { mutableStateOf(false) }
    var cameraPermanentlyDenied by remember { mutableStateOf(false) }

    val launcher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { grants ->
        grantsVersion++
        if (acceptPending) {
            // An ACCEPT-tap request decides the acceptance ALONE — the
            // entry-pass denial handling must not run here, or a mixed
            // result races a decline against the acceptance.
            acceptPending = false
            cameraPermanentlyDenied = resolveAcceptResult(context, viewModel, state, grants)
        } else if (grants[Manifest.permission.RECORD_AUDIO] == false) {
            viewModel.onPermissionsDenied()
        } else if (grants.values.all { it }) {
            viewModel.onReady()
        }
    }

    // Entry pass: MICROPHONE only — plus the camera solely when the user
    // themselves chose an outgoing video call. An incoming video invite
    // must not trigger a camera prompt before an explicit Accept gesture.
    LaunchedEffect(Unit) {
        launcher.launch(callPermissionSet(video = viewModel.outgoingVideo))
    }

    // Returning from Settings (or any resume) re-reads the REAL grants so
    // Accept can proceed without restarting the app.
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) grantsVersion++
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }
    LaunchedEffect(cameraGranted) {
        if (cameraGranted) cameraPermanentlyDenied = false
    }

    return CallPermissionFlow(
        showOpenSettings = cameraPermanentlyDenied && !cameraGranted &&
            (state as? CallState.Incoming)?.video == true,
        onAccept = {
            // The ACCEPT boundary (CALL-LB-6): current mic AND (for video)
            // camera state, read live; anything missing is requested from
            // this very tap — never accepted from a remembered value.
            val incomingVideo = (state as? CallState.Incoming)?.video == true
            if (acceptGrantsMissing(context, incomingVideo)) {
                acceptPending = true
                launcher.launch(callPermissionSet(video = incomingVideo))
            } else {
                viewModel.accept(context.hasPermission(Manifest.permission.CAMERA))
            }
        },
        onOpenSettings = { context.openAppSettings() },
    )
}

/** Mic always; camera only when this request must also cover video. */
private fun callPermissionSet(video: Boolean): Array<String> = if (video) {
    arrayOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.CAMERA)
} else {
    arrayOf(Manifest.permission.RECORD_AUDIO)
}

/** The ACCEPT boundary check on LIVE Android state (CALL-LB-6). */
private fun acceptGrantsMissing(context: Context, video: Boolean): Boolean {
    val mic = context.hasPermission(Manifest.permission.RECORD_AUDIO)
    val camera = context.hasPermission(Manifest.permission.CAMERA)
    return !mic || (video && !camera)
}

/**
 * Decides an Accept-launched permission result: mic denial or camera denial
 * keeps the call Incoming (with its reason line); a satisfied set continues
 * the acceptance exactly once. Returns whether the camera is now known to be
 * PERMANENTLY denied.
 */
private fun resolveAcceptResult(
    context: Context,
    viewModel: CallViewModel,
    state: CallState,
    grants: Map<String, Boolean>,
): Boolean {
    val micNow = context.hasPermission(Manifest.permission.RECORD_AUDIO)
    val cameraNow = context.hasPermission(Manifest.permission.CAMERA)
    val wantsCamera = (state as? CallState.Incoming)?.video == true
    return when {
        !micNow -> {
            viewModel.onMicDenied()
            false
        }
        wantsCamera && !cameraNow -> {
            viewModel.onCameraDenied()
            grants.containsKey(Manifest.permission.CAMERA) &&
                !context.shouldShowCameraRationale()
        }
        else -> {
            viewModel.accept(cameraNow)
            false
        }
    }
}

@Composable
private fun CallStateContent(
    state: CallState,
    idleMessage: String?,
    viewModel: CallViewModel,
    onAccept: () -> Unit,
    showOpenSettings: Boolean,
    onOpenSettings: () -> Unit,
) {
    when (state) {
        is CallState.Idle -> CenteredStatus(idleMessage ?: "Starting…")
        is CallState.Outgoing -> RingingContent(
            title = viewModel.peerName.ifBlank { "Calling…" },
            subtitle = if (state.video) "Video call · ringing" else "Ringing…",
            onCancel = viewModel::hangUp,
        )
        is CallState.Incoming -> IncomingContent(
            video = state.video,
            onAccept = onAccept,
            onDecline = viewModel::decline,
            showOpenSettings = showOpenSettings,
            onOpenSettings = onOpenSettings,
        )
        is CallState.Connecting -> RingingContent(
            title = viewModel.peerName.ifBlank { "Connecting…" },
            subtitle = "Connecting…",
            onCancel = viewModel::hangUp,
        )
        is CallState.Active -> ActiveContent(
            state = state,
            peerName = viewModel.peerName,
            viewModel = viewModel,
        )
        is CallState.Ended -> CenteredStatus(state.reason.userLine())
    }
}

@Composable
private fun CenteredStatus(text: String) {
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(text, color = Color.White, style = MaterialTheme.typography.titleLarge)
    }
}

@Composable
private fun RingingContent(title: String, subtitle: String, onCancel: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.SpaceBetween,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(title, color = Color.White, style = MaterialTheme.typography.headlineMedium)
            Text(subtitle, color = Color.Gray, style = MaterialTheme.typography.bodyLarge)
            CircularProgressIndicator(modifier = Modifier.padding(top = 24.dp).size(28.dp))
        }
        EndCallButton(onClick = onCancel, tag = "call-cancel")
    }
}

@Composable
private fun IncomingContent(
    video: Boolean,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
    showOpenSettings: Boolean,
    onOpenSettings: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.SpaceBetween,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = if (video) "Incoming video call" else "Incoming call",
                color = Color.White,
                style = MaterialTheme.typography.headlineMedium,
            )
            if (showOpenSettings) {
                // The camera was permanently denied: the system dialog will
                // no longer appear, so answering this VIDEO call needs the
                // grant flipped in Settings. The call keeps ringing.
                Button(
                    onClick = onOpenSettings,
                    modifier = Modifier.padding(top = 16.dp).testTag("call-open-settings"),
                ) { Text("Open Settings") }
            }
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceEvenly,
        ) {
            Button(
                onClick = onDecline,
                colors = ButtonDefaults.buttonColors(containerColor = DANGER_RED),
                modifier = Modifier.testTag("call-decline"),
            ) { Text("Decline") }
            Button(
                onClick = onAccept,
                colors = ButtonDefaults.buttonColors(containerColor = ACCEPT_GREEN),
                modifier = Modifier.testTag("call-accept"),
            ) { Text("Accept") }
        }
    }
}

@Composable
private fun ActiveContent(
    state: CallState.Active,
    peerName: String,
    viewModel: CallViewModel,
) {
    Box(modifier = Modifier.fillMaxSize()) {
        if (state.video) {
            CallVideoLayer(
                engine = viewModel.manager.currentEngine(),
                remoteAvailable = state.remoteVideoAvailable,
                localEnabled = state.videoEnabled,
            )
        }
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(24.dp),
            verticalArrangement = Arrangement.SpaceBetween,
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text(
                    text = peerName.ifBlank { "In call" },
                    color = Color.White,
                    style = MaterialTheme.typography.titleLarge,
                )
                ElapsedTime(state.startedAtMillis)
            }
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    ControlToggle("mute", state.muted, viewModel::toggleMute, "🎙")
                    ControlToggle("speaker", state.speakerOn, viewModel::toggleSpeaker, "🔊")
                    if (state.video) {
                        ControlToggle("video", !state.videoEnabled, viewModel::toggleVideo, "🎥")
                        IconButton(
                            onClick = viewModel::switchCamera,
                            modifier = Modifier.testTag("call-flip"),
                        ) { Text("🔄", color = Color.White) }
                    }
                }
                EndCallButton(onClick = viewModel::hangUp, tag = "call-end")
            }
        }
    }
}

@Composable
private fun ControlToggle(tag: String, checked: Boolean, onToggle: () -> Unit, glyph: String) {
    FilledIconToggleButton(
        checked = checked,
        onCheckedChange = { onToggle() },
        modifier = Modifier.testTag("call-$tag"),
    ) { Text(glyph) }
}

@Composable
private fun EndCallButton(onClick: () -> Unit, tag: String) {
    Button(
        onClick = onClick,
        colors = ButtonDefaults.buttonColors(containerColor = DANGER_RED),
        modifier = Modifier.padding(top = 16.dp).testTag(tag),
    ) { Text("End call") }
}

@Composable
private fun ElapsedTime(startedAtMillis: Long) {
    var seconds by remember { androidx.compose.runtime.mutableLongStateOf(0L) }
    LaunchedEffect(startedAtMillis) {
        while (true) {
            seconds = (System.currentTimeMillis() - startedAtMillis) / MILLIS_PER_SECOND
            delay(TICK_MILLIS)
        }
    }
    Text(
        text = "%d:%02d".format(seconds / SECONDS_PER_MINUTE, seconds % SECONDS_PER_MINUTE),
        color = Color.Gray,
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.testTag("call-elapsed"),
    )
}

private fun com.us.android.core.call.CallEndReason.userLine(): String = when (this) {
    com.us.android.core.call.CallEndReason.HungUp -> "Call ended"
    com.us.android.core.call.CallEndReason.RemoteEnded -> "Call ended"
    com.us.android.core.call.CallEndReason.Declined -> "Call declined"
    com.us.android.core.call.CallEndReason.Busy -> "Busy"
    com.us.android.core.call.CallEndReason.NoAnswer -> "No answer"
    com.us.android.core.call.CallEndReason.Missed -> "Missed call"
    com.us.android.core.call.CallEndReason.Failed -> "Call failed"
    com.us.android.core.call.CallEndReason.NotAllowed -> "This call isn't available"
}

private fun Context.hasPermission(permission: String): Boolean =
    ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED

private fun Context.shouldShowCameraRationale(): Boolean =
    findActivity()?.let {
        ActivityCompat.shouldShowRequestPermissionRationale(it, Manifest.permission.CAMERA)
    } ?: false

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}

private fun Context.openAppSettings() {
    startActivity(
        Intent(
            Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
            Uri.fromParts("package", packageName, null),
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
    )
}

private const val DANGER_RED_ARGB = 0xFFB3261E
private const val ACCEPT_GREEN_ARGB = 0xFF2E7D32
private val DANGER_RED = Color(DANGER_RED_ARGB)
private val ACCEPT_GREEN = Color(ACCEPT_GREEN_ARGB)
private const val TICK_MILLIS = 1_000L
private const val BACKDROP = 0xFF101418
private const val ENDED_CARD_MILLIS = 1_800L
private const val MILLIS_PER_SECOND = 1_000L
private const val SECONDS_PER_MINUTE = 60
