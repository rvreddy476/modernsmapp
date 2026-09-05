package com.us.android.feature.post.createhub.studio

import android.graphics.Bitmap
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.post.createhub.CoverFrame
import com.us.android.feature.post.createhub.Filmstrip
import com.us.android.feature.post.createhub.ReelFrameExtractor
import com.us.android.feature.post.createhub.ReelFrameSeeker
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.File
import javax.inject.Inject

/** The studio's five tools, in the order the rail shows them. */
enum class StudioTool(val label: String) {
    FRAME("Frame"),
    TRIM("Trim"),
    SPEED("Speed"),
    LOOKS("Looks"),
    TEXT("Text"),
}

/**
 * The Reel studio (founder, 2026-09-05): a picked video becomes a
 * [ReelEdit] — framed to 9:16, trimmed, sped, graded, captioned with one
 * line — and "Next" renders it through the [ReelExporter] into the file
 * the publish worker will upload. The details step takes over from there
 * with the EXPORTED file, so its cover picker scrubs what will be posted.
 *
 * Long videos never come here: they keep pick → form → upload.
 */
@HiltViewModel
@Suppress("TooManyFunctions") // One tool per group of functions; the studio IS the list of its tools.
class ReelStudioViewModel @Inject constructor(
    private val sources: ReelSourceReader,
    private val frames: ReelFrameExtractor,
    private val seeker: ReelFrameSeeker,
    private val exporter: ReelExporter,
) : ViewModel() {

    /** The export while it runs: whole percents, as the sheet shows them. */
    data class Export(val percent: Int)

    data class StudioState(
        /** The edit in progress; null before a source is picked, or while it is read. */
        val edit: ReelEdit? = null,
        /** The picked URI, kept while the header is read so the screen can show something. */
        val sourceUri: String? = null,
        val reading: Boolean = false,
        /** The source could not be read: the studio says so and offers the picker again. */
        val unreadable: Boolean = false,
        /** The trim strip's thumbnails, from the source. */
        val frames: List<CoverFrame> = emptyList(),
        /** The first frame: the looks row's live thumbnail, and Fit's blurred backdrop on the preview. */
        val thumbnail: Bitmap? = null,
        val tool: StudioTool = StudioTool.FRAME,
        val playing: Boolean = true,
        val export: Export? = null,
        val exportError: String? = null,
        /** The rendered file, once. The surface hands it to the details step and clears it. */
        val exportedPath: String? = null,
    ) {
        val isExporting: Boolean
            get() = export != null

        /** "Next" needs an edit that fits the reel cap and nothing already running. */
        val canExport: Boolean
            get() = edit != null && !edit.exceedsReelCap && export == null
    }

    private val _state = MutableStateFlow(StudioState())
    val state: StateFlow<StudioState> = _state.asStateFlow()

    private var readJob: Job? = null
    private var exportJob: Job? = null

    // ── Source ──────────────────────────────────────────────────────────

    /** A video was picked or recorded: read its header, pull the strip's frames, take the first frame. */
    fun setSource(uri: String) {
        readJob?.cancel()
        exportJob?.cancel()
        _state.value = StudioState(sourceUri = uri, reading = true)
        readJob = viewModelScope.launch {
            val source = sources.read(uri)
            if (source == null) {
                _state.update { it.copy(reading = false, unreadable = true) }
                return@launch
            }
            val edit = ReelEdit(uri, source.width, source.height, source.durationUs)
            _state.update { it.copy(edit = edit, reading = false) }
            launch {
                val strip = frames.extract(uri, Filmstrip.FRAME_COUNT)
                _state.update { if (it.sourceUri == uri) it.copy(frames = strip) else it }
            }
            launch {
                val first = seeker.frameAt(uri, 0L)
                _state.update { if (it.sourceUri == uri) it.copy(thumbnail = first) else it }
            }
        }
    }

    /** Back to nothing — "Change video", or leaving. */
    fun clear() {
        readJob?.cancel()
        exportJob?.cancel()
        _state.value = StudioState()
    }

    fun selectTool(tool: StudioTool) = _state.update { it.copy(tool = tool) }

    fun togglePlaying() = _state.update { it.copy(playing = !it.playing) }

    fun setPlaying(playing: Boolean) = _state.update { it.copy(playing = playing) }

    // ── Frame ───────────────────────────────────────────────────────────

    fun setMode(mode: FrameMode) = editing { it.withMode(mode) }

    /** A drag across the preview: [dragPx] along the free axis, over a preview [previewPx] long. */
    fun pan(dragPx: Float, previewPx: Float) = editing { it.panned(dragPx, previewPx) }

    // ── Trim ────────────────────────────────────────────────────────────

    fun setTrimStart(timeUs: Long) = editing { it.withTrimStart(timeUs) }

    fun setTrimEnd(timeUs: Long) = editing { it.withTrimEnd(timeUs) }

    // ── Speed, look ─────────────────────────────────────────────────────

    fun setSpeed(speed: ReelSpeed) = editing { it.copy(speed = speed) }

    fun setLook(look: ReelLook) = editing { it.copy(look = look) }

    // ── Text ────────────────────────────────────────────────────────────

    /** The one line; blank removes the pill. Kept to one line whatever was pasted. */
    fun setText(text: String) = editing { edit ->
        val line = text.replace(LINE_BREAKS, " ").take(MAX_TEXT_LENGTH)
        when {
            line.isBlank() -> edit.copy(text = null)
            edit.text == null -> edit.copy(text = TextPill(line))
            else -> edit.copy(text = edit.text.copy(text = line))
        }
    }

    fun setTextStyle(style: TextPillStyle) = editing { edit ->
        edit.text?.let { edit.copy(text = it.copy(style = style)) } ?: edit
    }

    /** The pill dragged: its centre as fractions of the frame, kept on it. */
    fun moveText(x: Float, y: Float) = editing { edit ->
        edit.text?.let { edit.copy(text = it.copy(x = x.coerceIn(0f, 1f), y = y.coerceIn(0f, 1f))) } ?: edit
    }

    fun removeText() = editing { it.copy(text = null) }

    // ── Export ──────────────────────────────────────────────────────────

    /** "Next": render to [targetPath]; the sheet shows the percent; Cancel stops it. */
    fun startExport(targetPath: String) {
        val edit = _state.value.edit ?: return
        if (!_state.value.canExport) return
        _state.update { it.copy(export = Export(0), exportError = null, playing = false) }
        exportJob = viewModelScope.launch {
            val outcome = exporter.export(edit, File(targetPath)) { percent ->
                _state.update { current -> current.export?.let { current.copy(export = Export(percent)) } ?: current }
            }
            _state.update { current ->
                when (outcome) {
                    is ExportOutcome.Done -> current.copy(export = null, exportedPath = outcome.path)
                    is ExportOutcome.Failed -> current.copy(export = null, exportError = outcome.message)
                    ExportOutcome.Cancelled -> current.copy(export = null)
                }
            }
        }
    }

    fun cancelExport() {
        exportJob?.cancel()
        exportJob = null
        _state.update { it.copy(export = null) }
    }

    fun dismissExportError() = _state.update { it.copy(exportError = null) }

    /** The surface took the exported file. */
    fun consumeExported() = _state.update { it.copy(exportedPath = null) }

    private inline fun editing(change: (ReelEdit) -> ReelEdit) = _state.update { current ->
        current.edit?.let { current.copy(edit = change(it)) } ?: current
    }

    companion object {
        /** One line of text, capped where a pill stops being a pill. */
        const val MAX_TEXT_LENGTH = 60

        private val LINE_BREAKS = Regex("[\\r\\n]+")
    }
}
