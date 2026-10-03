package com.us.android.feature.dating.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.clips.CODE_MECHANIC_NOT_ENABLED
import com.us.android.feature.dating.clips.CODE_NOT_READY
import com.us.android.feature.dating.clips.ClipCopy
import com.us.android.feature.dating.clips.ClipKind
import com.us.android.feature.dating.clips.ClipRules
import com.us.android.feature.dating.clips.ClipSource
import com.us.android.feature.dating.clips.ClipStatus
import com.us.android.feature.dating.clips.ClipUploader
import com.us.android.feature.dating.clips.OwnClipUi
import com.us.android.feature.dating.clips.VideoDurationReader
import com.us.android.feature.dating.clips.VoiceRecorder
import com.us.android.feature.dating.clips.clipRefusal
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.PromptAnswerDto
import com.us.android.feature.dating.network.PromptCatalogItemDto
import com.us.android.feature.dating.network.PromptClipViewDto
import com.us.android.feature.dating.photos.UploadOutcome
import com.us.android.feature.dating.ui.errorMessage
import com.us.android.feature.dating.ui.infoMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.File
import javax.inject.Inject

/** What the clip side of the editor is doing; one thing at a time, for one prompt. */
sealed interface ClipWork {
    val promptId: Int

    /** The microphone is on: [secondsLeft] counts down to the hard stop at 30 s. */
    data class Recording(override val promptId: Int, val secondsLeft: Int) : ClipWork

    /** A clip is being checked or uploaded; [progress] 0..1. */
    data class Uploading(override val promptId: Int, val kind: ClipKind, val progress: Float = 0f) : ClipWork

    /** The upload is done and the clip is being attached (CLIP_NOT_READY is retried here). */
    data class Attaching(override val promptId: Int) : ClipWork

    data class Removing(override val promptId: Int) : ClipWork
}

data class PromptsUiState(
    val loading: Boolean = true,
    val catalog: List<PromptCatalogItemDto> = emptyList(),
    /** Text answers by prompt id; "" for a clip-only answer. */
    val answers: Map<Int, String> = emptyMap(),
    val saving: Boolean = false,
    val message: UsMessage? = null,
    /** Mechanic M15: the owner's clips by prompt id. */
    val clips: Map<Int, OwnClipUi> = emptyMap(),
    /** False once the server said `MECHANIC_NOT_ENABLED`: no clip controls at all. */
    val clipsEnabled: Boolean = true,
    val clipWork: ClipWork? = null,
    /** The prompt waiting on the microphone explanation, before the system asks. */
    val micRationaleFor: Int? = null,
) {
    val clipBusy: Boolean get() = clipWork != null
}

/**
 * Optional prompts: up to the catalog's questions, each text answer ≤ 280
 * bytes (checked the way the server checks), and — mechanic M15 — a voice or
 * video clip of up to 30 seconds on any of them.
 *
 * A voice answer is recorded only after the microphone was explained and
 * granted, stops itself at 30 s, and its temp file is deleted once uploaded or
 * cancelled. A picked video longer than 30 s is refused here, before upload.
 */
@HiltViewModel
class PromptsViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val uploader: ClipUploader,
    private val recorder: VoiceRecorder,
    private val durations: VideoDurationReader,
) : ViewModel() {

    private val _state = MutableStateFlow(PromptsUiState(clipsEnabled = !session.isMechanicDisabled(ClipRules.MECHANIC)))
    val state: StateFlow<PromptsUiState> = _state.asStateFlow()

    /** The voice answer being written, owned here until it is uploaded or thrown away. */
    private var recording: File? = null
    private var countdown: Job? = null

    init {
        viewModelScope.launch {
            val catalog = (repository.promptCatalog() as? DatingResult.Success)?.value.orEmpty()
            val answers = (repository.prompts() as? DatingResult.Success)?.value.orEmpty()
            _state.update {
                it.copy(
                    loading = false,
                    catalog = catalog,
                    answers = answers.associate { a -> a.promptId to a.answer },
                    clips = answers.clips(),
                )
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun answer(promptId: Int, text: String) {
        val trimmed = text.trim()
        if (!fits(trimmed)) {
            _state.update { it.copy(message = errorMessage("Keep your answer under 280 characters.")) }
            return
        }
        // The server keeps a clip-only answer, but cannot clear the words of
        // one that has a clip without dropping the clip with them.
        if (trimmed.isEmpty() && _state.value.clips.containsKey(promptId)) {
            _state.update { it.copy(message = errorMessage(KEEP_WORDS)) }
            return
        }
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            val result = if (trimmed.isEmpty()) {
                repository.deletePrompt(promptId).let { r -> if (r is DatingResult.Success) null else r }
            } else {
                repository.answerPrompt(promptId, trimmed).let { r -> if (r is DatingResult.Success) null else r }
            }
            if (result is DatingResult.Failure) {
                _state.update { it.copy(saving = false, message = DatingCopy.message(result.error)) }
            } else {
                _state.update {
                    val answers = if (trimmed.isEmpty()) it.answers - promptId else it.answers + (promptId to trimmed)
                    it.copy(saving = false, answers = answers, message = successMessage("Saved."))
                }
            }
        }
    }

    // ── Voice ────────────────────────────────────────────────────────────────

    /**
     * "Record voice". [micPermitted] is whether RECORD_AUDIO is granted right
     * now: without it the explanation comes first, and nothing is recorded.
     */
    fun recordVoice(promptId: Int, micPermitted: Boolean) {
        val s = _state.value
        if (!s.clipsEnabled || s.clipBusy || s.micRationaleFor != null) return
        if (!micPermitted) {
            _state.update { it.copy(micRationaleFor = promptId) }
            return
        }
        startRecording(promptId)
    }

    /** The explanation was closed without asking the system. */
    fun dismissMicRationale() = _state.update { it.copy(micRationaleFor = null) }

    /** The system's answer to the microphone request the explanation led to. */
    fun micPermissionResult(granted: Boolean) {
        val promptId = _state.value.micRationaleFor ?: return
        _state.update { it.copy(micRationaleFor = null) }
        if (granted) startRecording(promptId) else _state.update { it.copy(message = errorMessage(ClipCopy.MIC_DENIED)) }
    }

    /** "Stop": the recording so far is uploaded. */
    fun stopRecording() {
        if (_state.value.clipWork !is ClipWork.Recording) return
        countdown?.cancel()
        countdown = null
        finishRecording()
    }

    /** "Cancel": nothing is kept, the temp file goes. */
    fun cancelRecording() {
        if (_state.value.clipWork !is ClipWork.Recording) return
        countdown?.cancel()
        countdown = null
        discardRecording()
        _state.update { it.copy(clipWork = null) }
    }

    private fun startRecording(promptId: Int) {
        if (_state.value.clipBusy) return
        val file = recorder.start()
        if (file == null) {
            _state.update { it.copy(message = errorMessage(ClipCopy.RECORD_FAILED)) }
            return
        }
        recording = file
        _state.update { it.copy(clipWork = ClipWork.Recording(promptId, ClipRules.MAX_RECORD_SECONDS)) }
        countdown = viewModelScope.launch {
            var left = ClipRules.MAX_RECORD_SECONDS
            while (left > 0) {
                // The last second is cut short: the microphone starts a beat
                // before the countdown, and the server refuses a clip over 30 s.
                delay(if (left == 1) LAST_SECOND_MILLIS else SECOND_MILLIS)
                left -= 1
                _state.update { s ->
                    val work = s.clipWork as? ClipWork.Recording ?: return@update s
                    s.copy(clipWork = work.copy(secondsLeft = left))
                }
            }
            // The hard stop: 30 s is the most a clip may be.
            countdown = null
            finishRecording()
        }
    }

    private fun finishRecording() {
        val work = _state.value.clipWork as? ClipWork.Recording ?: return
        val file = recording ?: return
        recording = null
        if (!recorder.stop()) {
            file.delete()
            _state.update { it.copy(clipWork = null, message = errorMessage(ClipCopy.RECORD_FAILED)) }
            return
        }
        upload(work.promptId, ClipSource.Recorded(file))
    }

    private fun discardRecording() {
        recorder.cancel()
        recording?.delete()
        recording = null
    }

    // ── Video ────────────────────────────────────────────────────────────────

    /** The system picker returned [uri] (null: nothing chosen). Over 30 s is refused before upload. */
    fun videoPicked(promptId: Int, uri: String?) {
        val s = _state.value
        if (uri.isNullOrBlank() || !s.clipsEnabled || s.clipBusy) return
        _state.update { it.copy(clipWork = ClipWork.Uploading(promptId, ClipKind.VIDEO)) }
        viewModelScope.launch {
            val length = durations.durationMs(uri)
            when {
                length == null -> failClip(ClipCopy.VIDEO_UNREADABLE)
                !ClipRules.fits(length) -> failClip(ClipCopy.VIDEO_TOO_LONG)
                else -> upload(promptId, ClipSource.Picked(uri))
            }
        }
    }

    // ── Upload, attach, remove ───────────────────────────────────────────────

    private fun upload(promptId: Int, source: ClipSource) {
        _state.update { it.copy(clipWork = ClipWork.Uploading(promptId, source.kind)) }
        viewModelScope.launch {
            try {
                val outcome = uploader.upload(source) { progress ->
                    _state.update { s ->
                        val work = s.clipWork as? ClipWork.Uploading ?: return@update s
                        if (work.promptId != promptId) s else s.copy(clipWork = work.copy(progress = progress.coerceIn(0f, 1f)))
                    }
                }
                when (outcome) {
                    is UploadOutcome.Failed -> failClip(outcome.message)
                    is UploadOutcome.Ready -> attach(promptId, outcome.mediaId, source.kind)
                }
            } finally {
                // A voice answer has no business lingering, whatever happened.
                (source as? ClipSource.Recorded)?.file?.delete()
            }
        }
    }

    /** `PUT /prompts/:promptId/clip`. CLIP_NOT_READY is retried with backoff before giving up. */
    private suspend fun attach(promptId: Int, mediaId: String, kind: ClipKind) {
        _state.update { it.copy(clipWork = ClipWork.Attaching(promptId)) }
        var retries = 0
        while (true) {
            when (val result = repository.putPromptClip(promptId, mediaId)) {
                is DatingResult.Success -> {
                    attached(promptId, result.value, kind)
                    return
                }
                is DatingResult.Failure -> {
                    if (result.error.code == CODE_NOT_READY && retries < NOT_READY_BACKOFF_MILLIS.size) {
                        delay(NOT_READY_BACKOFF_MILLIS[retries])
                        retries += 1
                    } else {
                        clipRefused(result.error)
                        return
                    }
                }
            }
        }
    }

    private fun attached(promptId: Int, dto: PromptClipViewDto, kind: ClipKind) {
        val clip = OwnClipUi(
            kind = ClipKind.fromWire(dto.kind) ?: kind,
            durationMs = dto.durationMs.coerceAtLeast(0L),
            status = ClipStatus.fromWire(dto.status),
            reason = dto.reason?.trim()?.takeIf { it.isNotEmpty() },
        )
        val note = when (clip.status) {
            ClipStatus.LIVE -> successMessage(ClipCopy.ADDED_LIVE)
            ClipStatus.CHECKING -> successMessage(ClipCopy.ADDED_CHECKING)
            ClipStatus.REJECTED -> errorMessage(clip.statusLine)
        }
        _state.update {
            it.copy(
                clipWork = null,
                clips = it.clips + (promptId to clip),
                // A clip with no words is still an answer.
                answers = it.answers + (promptId to it.answers[promptId].orEmpty()),
                message = note,
            )
        }
    }

    /** "Remove clip": `DELETE /prompts/:promptId/clip`. A clip-only answer goes with it. */
    fun removeClip(promptId: Int) {
        val s = _state.value
        if (s.clipBusy || !s.clips.containsKey(promptId)) return
        _state.update { it.copy(clipWork = ClipWork.Removing(promptId)) }
        viewModelScope.launch {
            when (val result = repository.deletePromptClip(promptId)) {
                is DatingResult.Success -> removed(promptId, successMessage(ClipCopy.REMOVED))
                is DatingResult.Failure -> when {
                    // Already gone on the server: so it is here too.
                    result.error.code == CODE_PROMPT_NOT_FOUND -> removed(promptId, successMessage(ClipCopy.REMOVED))
                    else -> clipRefused(result.error)
                }
            }
        }
    }

    private fun removed(promptId: Int, note: UsMessage) = _state.update {
        val words = it.answers[promptId].orEmpty()
        it.copy(
            clipWork = null,
            clips = it.clips - promptId,
            answers = if (words.isBlank()) it.answers - promptId else it.answers,
            message = note,
        )
    }

    private fun clipRefused(error: DatingError) {
        if (error.code == CODE_MECHANIC_NOT_ENABLED) {
            session.disableMechanic(ClipRules.MECHANIC)
            _state.update { it.copy(clipWork = null, clipsEnabled = false, message = infoMessage(ClipCopy.NOT_ENABLED)) }
            return
        }
        failClip(clipRefusal(error, repository.json))
    }

    private fun failClip(text: String) = _state.update { it.copy(clipWork = null, message = errorMessage(text)) }

    override fun onCleared() {
        countdown?.cancel()
        if (recording != null) discardRecording()
        super.onCleared()
    }

    private fun List<PromptAnswerDto>.clips(): Map<Int, OwnClipUi> =
        mapNotNull { a -> ClipRules.ownClip(a)?.let { a.promptId to it } }.toMap()

    companion object {
        /** The server measures bytes after trimming, not characters. */
        const val MAX_ANSWER_BYTES = 280

        /** Waits before each retry of a `CLIP_NOT_READY` attach: three retries, then the refusal is said. */
        val NOT_READY_BACKOFF_MILLIS = listOf(2_000L, 4_000L, 8_000L)

        const val KEEP_WORDS = "An answer with a clip keeps its words. Remove the clip first to clear it."

        private const val SECOND_MILLIS = 1_000L

        /** The hard stop lands at 29.6 s of countdown, safely inside the server's 30 s. */
        const val LAST_SECOND_MILLIS = 600L
        private const val CODE_PROMPT_NOT_FOUND = "NOT_FOUND"

        fun fits(answer: String): Boolean = answer.trim().toByteArray(Charsets.UTF_8).size <= MAX_ANSWER_BYTES
    }
}
