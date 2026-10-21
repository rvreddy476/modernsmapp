package com.us.android.core.feed.ui.audio

import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.AudioTrack
import com.us.android.core.feed.data.AudioTracksRepository
import com.us.android.core.feed.data.MAX_AUDIO_TRACKS
import com.us.android.core.feed.data.audioLanguageLabel
import com.us.android.core.feed.data.audioTrackRefusal
import com.us.android.core.feed.data.isDubbingUnavailable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The creator's dub manager, as the sheet draws it. */
data class AudioTracksUiState(
    /** The first read is on the wire and nothing is known yet. */
    val loading: Boolean = true,
    /** Every track besides the original, oldest first. */
    val tracks: List<AudioTrack> = emptyList(),
    /** The language the next upload or generate is for. */
    val language: String = DEFAULT_LANGUAGE,
    val label: String = "",
    /** The picked file's name; null until one is picked. */
    val fileName: String? = null,
    val uploading: Boolean = false,
    val generating: Boolean = false,
    /** The track whose delete is on the wire. */
    val deletingId: String? = null,
    /** The server said DUBBING_UNAVAILABLE: Generate is withdrawn, uploads still work. */
    val dubbingUnavailable: Boolean = false,
    /** A one-line confirmation ("Hindi track uploaded"). */
    val notice: String? = null,
    /** Why the last action was refused, in the web's words. */
    val error: String? = null,
) {
    /** Languages that already have a track: each language carries one (the server replaces). */
    val taken: Set<String> get() = tracks.mapTo(HashSet()) { it.language.lowercase() }

    val full: Boolean get() = tracks.size >= MAX_AUDIO_TRACKS

    val canUpload: Boolean get() = fileName != null && !full && !uploading && language !in taken

    val canGenerate: Boolean get() = !full && !generating && !dubbingUnavailable && language !in taken
}

private const val DEFAULT_LANGUAGE = "hi"

/**
 * Behind [AudioTracksSheet] (2026-10-21, the web's `ReelAudioTracksDialog`):
 * the creator's tracks for one video with their state, an upload (one audio
 * file for one language), Generate for an AI dub when the server offers one,
 * and Delete. Polls every five seconds while a track is still being made, so
 * "Ready" lands without reopening the sheet.
 */
@HiltViewModel
class AudioTracksViewModel @Inject constructor(
    private val repository: AudioTracksRepository,
    private val files: AudioFileReader,
) : ViewModel() {

    private val mediaId = MutableStateFlow("")
    private val form = MutableStateFlow(AudioTracksUiState())
    private var picked: PickedAudio? = null
    private var poll: Job? = null

    val state: StateFlow<AudioTracksUiState> = combine(form, repository.tracks, mediaId) { form, all, id ->
        val known = all[id]
        form.copy(
            loading = known == null && form.loading,
            tracks = known.orEmpty().filterNot { it.isOriginal },
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), AudioTracksUiState())

    /** The sheet opened for [id]: read it fresh, and keep reading while anything is still being made. */
    fun bind(id: String) {
        if (id.isBlank()) return
        mediaId.value = id
        form.value = AudioTracksUiState()
        picked = null
        poll?.cancel()
        poll = viewModelScope.launch {
            val first = repository.refresh(id)
            val refusal = (first as? AppResult.Failure)?.let { audioTrackRefusal(it.error) }
            form.update { it.copy(loading = false, error = refusal) }
            while (isActive) {
                delay(POLL_MILLIS)
                val working = repository.tracks.value[id].orEmpty().any { it.status.working }
                if (working) repository.refresh(id)
            }
        }
    }

    /** The sheet closed: the polling stops with it. */
    fun unbind() {
        poll?.cancel()
        poll = null
    }

    /** What [state] would read now, whether or not the sheet is collecting it. */
    private fun current(): AudioTracksUiState =
        form.value.copy(tracks = repository.tracks.value[mediaId.value].orEmpty().filterNot { it.isOriginal })

    fun selectLanguage(tag: String) = form.update { it.copy(language = tag, error = null, notice = null) }

    fun onLabelChanged(label: String) = form.update { it.copy(label = label.take(LABEL_MAX)) }

    /** A file came back from the picker. One over 50 MB is refused here rather than after the upload. */
    fun onFilePicked(uri: Uri?) {
        if (uri == null) return
        val file = files.read(uri)
        if (file == null) {
            form.update { it.copy(error = "That file can't be read. Pick another.") }
            return
        }
        if ((file.sizeBytes ?: 0L) > MAX_UPLOAD_BYTES) {
            form.update { it.copy(error = "That file is too large (50 MB max).") }
            return
        }
        picked = file
        form.update { it.copy(fileName = file.file.name, error = null, notice = null) }
    }

    fun upload() {
        val id = mediaId.value
        val file = picked ?: return
        val now = form.value
        if (!current().canUpload) return
        form.update { it.copy(uploading = true, error = null, notice = null) }
        viewModelScope.launch {
            val result = repository.upload(id, file.file, now.language, now.label)
            form.update {
                when (result) {
                    is AppResult.Success -> {
                        picked = null
                        val language = audioLanguageLabel(now.language)
                        it.copy(
                            uploading = false,
                            fileName = null,
                            label = "",
                            notice = "$language track uploaded. It will be ready to play in a minute.",
                        )
                    }
                    is AppResult.Failure -> it.copy(uploading = false, error = audioTrackRefusal(result.error))
                }
            }
        }
    }

    fun generate() {
        val id = mediaId.value
        val language = form.value.language
        if (!current().canGenerate) return
        form.update { it.copy(generating = true, error = null, notice = null) }
        viewModelScope.launch {
            val result = repository.generate(id, language)
            form.update {
                when (result) {
                    is AppResult.Success -> it.copy(
                        generating = false,
                        notice = "Generating a ${audioLanguageLabel(language)} dub. This takes a few minutes.",
                    )
                    is AppResult.Failure -> it.copy(
                        generating = false,
                        dubbingUnavailable = it.dubbingUnavailable || result.error.isDubbingUnavailable(),
                        error = audioTrackRefusal(result.error),
                    )
                }
            }
        }
    }

    fun delete(trackId: String) {
        val id = mediaId.value
        if (form.value.deletingId != null) return
        form.update { it.copy(deletingId = trackId, error = null, notice = null) }
        viewModelScope.launch {
            val result = repository.delete(id, trackId)
            form.update {
                it.copy(
                    deletingId = null,
                    error = (result as? AppResult.Failure)?.let { failure ->
                        audioTrackRefusal(failure.error, fallback = "Couldn't delete that track. Try again.")
                    },
                )
            }
        }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
        const val POLL_MILLIS = 5_000L
        const val LABEL_MAX = 60
        const val MAX_UPLOAD_BYTES = 50L * 1024 * 1024
    }
}
