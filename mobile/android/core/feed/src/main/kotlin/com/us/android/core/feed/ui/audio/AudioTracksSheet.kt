package com.us.android.core.feed.ui.audio

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.AUDIO_LANGUAGE_CHOICES
import com.us.android.core.feed.data.AudioTrack
import com.us.android.core.feed.data.AudioTrackSource
import com.us.android.core.feed.data.AudioTrackStatus
import com.us.android.core.feed.data.DUBBING_UNAVAILABLE_MESSAGE
import com.us.android.core.feed.data.MAX_AUDIO_TRACKS
import com.us.android.core.feed.data.audioLanguageLabel
import com.us.android.core.feed.ui.edit.OwnerSheet
import com.us.android.core.feed.ui.edit.SheetNote
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSelectRow

/**
 * "Audio tracks" — the creator's dub manager for one video (2026-10-21, the
 * web's `ReelAudioTracksDialog`), opened from the More menu of their OWN reel
 * or long video. Every track with its state and a Delete; an upload of one
 * audio file for one language, with an optional label; and "Generate" for an
 * AI dub, withdrawn with a clear line once the server says it has none
 * (503 DUBBING_UNAVAILABLE). Viewers pick between ready tracks from the
 * video's own More menu.
 *
 * One ViewModel per video ([mediaId] keys it), so reopening the sheet on
 * another video never shows the last one's form.
 */
@Composable
fun AudioTracksSheet(
    mediaId: String,
    onDismiss: () -> Unit,
    viewModel: AudioTracksViewModel = hiltViewModel(key = "audio-tracks:$mediaId"),
) {
    DisposableEffect(mediaId) {
        viewModel.bind(mediaId)
        onDispose { viewModel.unbind() }
    }
    val state by viewModel.state.collectAsStateWithLifecycle()
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.GetContent(), viewModel::onFilePicked)

    OwnerSheet(
        title = "Audio tracks",
        subtitle = "Viewers pick a language from the video's menu. Upload your own dub, or let us generate one.",
        icon = UsIcons.AudioLines,
        testTag = "audio_tracks_sheet",
        onDismiss = onDismiss,
    ) {
        TrackList(state = state, onDelete = viewModel::delete)
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        UsSettingsSelectRow(
            title = "Language",
            selected = state.language,
            options = AUDIO_LANGUAGE_CHOICES.map { language ->
                val added = if (language.tag in state.taken) " · added" else ""
                UsSettingsOption(language.tag, language.label + added)
            },
            onSelected = viewModel::selectLanguage,
            description = LANGUAGE_TAKEN.takeIf { state.language in state.taken },
            modifier = Modifier.testTag("audio_tracks_language"),
        )
        UsTextField(
            value = state.label,
            onValueChange = viewModel::onLabelChanged,
            label = "Label (optional)",
            placeholder = audioLanguageLabel(state.language),
            modifier = Modifier.fillMaxWidth().testTag("audio_tracks_label"),
        )
        UsSecondaryButton(
            text = state.fileName?.let { "Audio file: $it" } ?: "Choose an audio file",
            onClick = { pick.launch(AUDIO_MIME) },
            enabled = !state.uploading,
            modifier = Modifier.fillMaxWidth().testTag("audio_tracks_pick"),
        )
        UsButton(
            text = "Upload track",
            onClick = viewModel::upload,
            enabled = state.canUpload,
            loading = state.uploading,
            modifier = Modifier.fillMaxWidth().testTag("audio_tracks_upload"),
        )
        UsSecondaryButton(
            text = "Generate ${audioLanguageLabel(state.language)} dub",
            onClick = viewModel::generate,
            enabled = state.canGenerate,
            modifier = Modifier.fillMaxWidth().testTag("audio_tracks_generate"),
        )
        Notes(state)
    }
}

/** The original, then each track with its state and a Delete. */
@Composable
private fun TrackList(state: AudioTracksUiState, onDelete: (String) -> Unit) {
    Column(modifier = Modifier.fillMaxWidth()) {
        TrackLine(name = "Original", meta = null, status = "Plays by default", testTag = "audio_tracks_track:original")
        state.tracks.forEach { track ->
            TrackLine(
                name = track.label.ifBlank { audioLanguageLabel(track.language) },
                meta = trackMeta(track),
                status = statusLabel(track.status),
                testTag = "audio_tracks_track:${track.id}",
                onDelete = { onDelete(track.id) }.takeIf { state.deletingId == null },
                deleteLabel = "Delete ${track.label.ifBlank { track.language }} track",
            )
        }
        if (state.loading) {
            Text(
                text = "Loading…",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(vertical = UsTheme.spacing.s),
            )
        }
    }
}

@Suppress("LongParameterList")
@Composable
private fun TrackLine(
    name: String,
    meta: String?,
    status: String,
    testTag: String,
    onDelete: (() -> Unit)? = null,
    deleteLabel: String = "",
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.s)
            .testTag(testTag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(text = name, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            if (meta != null) {
                Text(text = meta, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        Text(text = status, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textSecondary)
        if (onDelete != null) {
            Icon(
                imageVector = UsIcons.Trash,
                contentDescription = null,
                tint = UsTheme.extended.textSecondary,
                modifier = Modifier
                    .size(DELETE_GLYPH)
                    .clickable(role = Role.Button, onClick = onDelete)
                    .semantics { contentDescription = deleteLabel }
                    .testTag("$testTag:delete"),
            )
        }
    }
}

/** "Uploaded · hi" / "Generated · te", as the web's meta line. */
private fun trackMeta(track: AudioTrack): String {
    val source = if (track.source == AudioTrackSource.GENERATED) "Generated" else "Uploaded"
    return "$source · ${track.language}"
}

private fun statusLabel(status: AudioTrackStatus): String = when (status) {
    AudioTrackStatus.READY -> "Ready"
    AudioTrackStatus.FAILED -> "Failed"
    AudioTrackStatus.PROCESSING -> "Processing"
    AudioTrackStatus.PENDING -> "Queued"
}

/** The one refusal or confirmation, then the two standing notes. */
@Composable
private fun Notes(state: AudioTracksUiState) {
    state.error?.let { SheetNote(text = it, isError = true, testTag = "audio_tracks_error") }
    state.notice?.let { SheetNote(text = it, isError = false, testTag = "audio_tracks_notice") }
    if (state.dubbingUnavailable && state.error != DUBBING_UNAVAILABLE_MESSAGE) {
        Text(
            text = DUBBING_UNAVAILABLE_MESSAGE,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.testTag("audio_tracks_dubbing_unavailable"),
        )
    }
    if (state.full) {
        Text(
            text = "This video has the maximum of $MAX_AUDIO_TRACKS audio tracks.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** Under the language while it already has a track: the web disables it, the server would replace it. */
private const val LANGUAGE_TAKEN = "This language already has a track. Delete it first."

/** What the picker offers: any audio file (m4a, mp3, wav, ogg, flac, aac). */
private const val AUDIO_MIME = "audio/*"

private val DELETE_GLYPH = 20.dp
