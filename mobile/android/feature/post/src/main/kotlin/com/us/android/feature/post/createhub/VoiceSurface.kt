package com.us.android.feature.post.createhub

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.createhub.VoicePublishViewModel.Phase
import com.us.android.feature.post.createhub.VoicePublishViewModel.VoiceSource
import kotlin.math.abs
import kotlin.math.sin

/**
 * The Audio surface: one big round Record button that becomes Stop, elapsed
 * time and a level meter while it runs, OR an audio file from the picker;
 * then a play/preview control, an optional caption, and Post.
 *
 * ## THE PERMISSION IS ASKED FOR ON RECORD, WITH A REASON
 *
 * Not on open: picking a file needs no microphone, and a prompt before the
 * user has expressed any intent is the prompt people decline. The first tap
 * of Record shows why the microphone is needed and a button that asks; a
 * refusal turns that card into directions to Settings and leaves the file
 * picker as the other way in.
 */
// LongMethod/complexity: one surface, one composable — the branches ARE the
// record → preview → upload → post state machine, rendered honestly.
@Suppress("LongMethod", "CyclomaticComplexMethod")
@Composable
internal fun VoiceSurface(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    viewModel: VoicePublishViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var explainingMic by remember { mutableStateOf(false) }

    val requestMic = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        explainingMic = false
        if (granted) viewModel.onStartRecording() else viewModel.onMicDenied()
    }

    val pickAudio = rememberLauncherForActivityResult(
        ActivityResultContracts.GetContent(),
    ) { uri -> uri?.let(viewModel::onAudioPicked) }

    (state.phase as? Phase.Published)?.let { published ->
        LaunchedEffect(published.postId) { onPublished(published.postId) }
    }

    fun onRecordTapped() {
        val granted = ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) ==
            PackageManager.PERMISSION_GRANTED
        when {
            state.recording -> viewModel.onStopRecording()
            granted -> viewModel.onStartRecording()
            else -> explainingMic = true
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        CreateTopBar(title = CreateSurface.Audio.label, onClose = onClose) {
            PublishPill(
                text = if (state.phase is Phase.Failure) "Try again" else "Post",
                enabled = state.canPost && !state.isBusy,
                busy = state.isBusy,
                onClick = viewModel::onPost,
                description = when {
                    state.isBusy -> "Post voice note. In progress."
                    state.canPost -> "Post voice note"
                    else -> "Post voice note. Unavailable: record or choose audio first."
                },
                testTag = "voice-post",
            )
        }

        Column(
            modifier = Modifier
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))

            when (val source = state.source) {
                null -> RecordPanel(
                    recording = state.recording,
                    elapsedMillis = state.elapsedMillis,
                    level = state.level,
                    onRecordTapped = ::onRecordTapped,
                )
                else -> ClipCard(
                    source = source,
                    playing = state.playing,
                    enabled = !state.isBusy,
                    onTogglePlayback = viewModel::onTogglePlayback,
                    onRemove = viewModel::onClearAudio,
                )
            }

            if (explainingMic || state.micDenied) {
                Spacer(Modifier.height(UsTheme.spacing.l))
                MicPermissionCard(
                    denied = state.micDenied,
                    onAllow = { requestMic.launch(Manifest.permission.RECORD_AUDIO) },
                    onDismiss = { explainingMic = false },
                )
            }

            if (state.source == null && !state.recording) {
                Spacer(Modifier.height(UsTheme.spacing.xxxxl))
                Text(
                    text = "or",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
                Spacer(Modifier.height(UsTheme.spacing.m))
                UsSecondaryButton(
                    text = "Choose an audio file",
                    onClick = { pickAudio.launch("audio/*") },
                    enabled = !state.isBusy,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("voice-pick"),
                )
            }

            if (state.source != null) {
                Spacer(Modifier.height(UsTheme.spacing.xl))
                OutlinedTextField(
                    value = state.caption,
                    onValueChange = viewModel::onCaptionChanged,
                    label = { Text("Caption (optional)") },
                    minLines = 2,
                    enabled = !state.isBusy,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("voice-caption"),
                )
                Spacer(Modifier.height(UsTheme.spacing.m))
                Text(
                    text = "Voice posts are checked before they appear publicly.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            Spacer(Modifier.height(UsTheme.spacing.l))
            PhaseStatus(phase = state.phase)
            Spacer(Modifier.height(UsTheme.spacing.xl))
        }
    }
}

// ── Record ──────────────────────────────────────────────────────────────

/** The big button, the clock and the meter. */
@Composable
private fun RecordPanel(
    recording: Boolean,
    elapsedMillis: Long,
    level: Float,
    onRecordTapped: () -> Unit,
) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            text = formatElapsed(elapsedMillis),
            style = MaterialTheme.typography.headlineMedium.copy(fontSize = CLOCK_SIZE),
            color = if (recording) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
            modifier = Modifier.testTag("voice-elapsed"),
        )
        Spacer(Modifier.height(UsTheme.spacing.m))
        LevelMeter(level = if (recording) level else 0f)
        Spacer(Modifier.height(UsTheme.spacing.xxxxl))

        Box(
            modifier = Modifier
                .size(RECORD_BUTTON)
                .clip(CircleShape)
                .background(UsTheme.extended.create.audio)
                .clickable(onClick = onRecordTapped)
                .semantics {
                    contentDescription = if (recording) "Stop recording" else "Record a voice note"
                    role = Role.Button
                }
                .testTag("voice-record"),
            contentAlignment = Alignment.Center,
        ) {
            if (recording) {
                // Stop: a white rounded square, the universal mark.
                Box(
                    modifier = Modifier
                        .size(STOP_GLYPH)
                        .clip(RoundedCornerShape(UsTheme.radii.small))
                        .background(Color.White),
                )
            } else {
                Icon(
                    imageVector = UsIcons.Mic,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(RECORD_GLYPH),
                )
            }
        }
        Spacer(Modifier.height(UsTheme.spacing.l))
        Text(
            text = if (recording) "Tap to stop · up to 3:00" else "Tap to record",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
    }
}

/**
 * Twenty bars shaped by the current peak. The per-bar weight is a fixed
 * sine so the meter has a silhouette rather than being a flat line that
 * rises and falls as one block.
 */
@Composable
private fun LevelMeter(level: Float) {
    Row(
        modifier = Modifier.height(METER_HEIGHT),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(METER_GAP),
    ) {
        repeat(METER_BARS) { index ->
            val weight = METER_FLOOR + (1f - METER_FLOOR) * abs(sin(index * METER_SHAPE))
            val height = METER_MIN + (METER_HEIGHT - METER_MIN) * level * weight
            Box(
                modifier = Modifier
                    .width(METER_BAR)
                    .height(height)
                    .clip(CircleShape)
                    .background(
                        if (level > 0f) UsTheme.extended.accentSolid else UsTheme.extended.borderMedium,
                    ),
            )
        }
    }
}

// ── The chosen clip ─────────────────────────────────────────────────────

/** What is about to be posted, with play/pause and a way out. */
@Composable
private fun ClipCard(
    source: VoiceSource,
    playing: Boolean,
    enabled: Boolean,
    onTogglePlayback: () -> Unit,
    onRemove: () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgRaised)
            .border(1.dp, UsTheme.extended.borderMedium, shape)
            .padding(UsTheme.spacing.xl),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .size(PLAY_BUTTON)
                .clip(CircleShape)
                .background(UsTheme.extended.create.audio)
                .clickable(onClick = onTogglePlayback)
                .semantics {
                    contentDescription = if (playing) "Pause preview" else "Play preview"
                    role = Role.Button
                }
                .testTag("voice-play"),
            contentAlignment = Alignment.Center,
        ) {
            if (playing) {
                Row(horizontalArrangement = Arrangement.spacedBy(PAUSE_GAP)) {
                    repeat(2) {
                        Box(
                            modifier = Modifier
                                .size(width = PAUSE_BAR, height = PAUSE_HEIGHT)
                                .clip(RoundedCornerShape(1.dp))
                                .background(Color.White),
                        )
                    }
                }
            } else {
                Icon(
                    imageVector = UsIcons.Play,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(PLAY_GLYPH),
                )
            }
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = when (source) {
                    is VoiceSource.Recorded -> "Voice note"
                    is VoiceSource.Picked -> source.displayName
                },
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
            )
            Text(
                text = when (source) {
                    is VoiceSource.Recorded -> formatElapsed(source.durationMillis)
                    is VoiceSource.Picked -> source.mimeType
                },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        TextButton(onClick = onRemove, enabled = enabled, modifier = Modifier.testTag("voice-remove")) {
            Text("Remove")
        }
    }
}

// ── Permission ──────────────────────────────────────────────────────────

/** Why the microphone, and the button that asks — or, after a refusal, where to fix it. */
@Composable
private fun MicPermissionCard(denied: Boolean, onAllow: () -> Unit, onDismiss: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .padding(UsTheme.spacing.xl)
            .testTag("voice-mic-rationale"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(
            text = if (denied) "Microphone access is off" else "Recording needs the microphone",
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = if (denied) {
                "Allow the microphone for this app in Settings to record, or choose an audio file instead."
            } else {
                "Your voice note is recorded on this phone and only uploaded when you tap Post."
            },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            if (!denied) {
                TextButton(onClick = onAllow, modifier = Modifier.testTag("voice-mic-allow")) {
                    Text("Allow microphone")
                }
            }
            TextButton(onClick = onDismiss) { Text(if (denied) "OK" else "Not now") }
        }
    }
}

// ── Status ──────────────────────────────────────────────────────────────

@Composable
private fun PhaseStatus(phase: Phase) {
    when (phase) {
        is Phase.Uploading -> {
            LinearProgressIndicator(progress = { phase.fraction }, modifier = Modifier.fillMaxWidth())
            StatusLine("Uploading audio… ${(phase.fraction * PERCENT).toInt()}%")
        }
        is Phase.Processing -> StatusLine("Checking the recording…")
        is Phase.Posting -> StatusLine("Posting…")
        is Phase.Failure -> Text(
            text = phase.message,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("voice-error"),
        )
        is Phase.Published, is Phase.Editing -> Unit
    }
}

@Composable
private fun StatusLine(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(vertical = UsTheme.spacing.s),
    )
}

/** m:ss, the way every recorder shows it. */
internal fun formatElapsed(millis: Long): String {
    val totalSeconds = millis / MILLIS_PER_SECOND
    val minutes = totalSeconds / SECONDS_PER_MINUTE
    val seconds = totalSeconds % SECONDS_PER_MINUTE
    return "$minutes:${seconds.toString().padStart(2, '0')}"
}

private const val PERCENT = 100
private const val MILLIS_PER_SECOND = 1_000L
private const val SECONDS_PER_MINUTE = 60L
private const val METER_BARS = 20
private const val METER_FLOOR = 0.35f
private const val METER_SHAPE = 0.7f

private val CLOCK_SIZE = 40.sp
private val RECORD_BUTTON = 96.dp
private val RECORD_GLYPH = 40.dp
private val STOP_GLYPH = 32.dp
private val PLAY_BUTTON = 48.dp
private val PLAY_GLYPH = 22.dp
private val PAUSE_BAR = 5.dp
private val PAUSE_HEIGHT = 18.dp
private val PAUSE_GAP = 4.dp
private val METER_HEIGHT = 40.dp
private val METER_MIN = 4.dp
private val METER_BAR = 4.dp
private val METER_GAP = 3.dp
