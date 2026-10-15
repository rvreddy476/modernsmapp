package com.us.android.core.feed.ui.sound

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.LIBRARY_UNAVAILABLE
import com.us.android.core.feed.data.LibrarySound
import com.us.android.core.feed.data.LibrarySoundsRepository
import com.us.android.core.feed.data.libraryGenres
import com.us.android.core.feed.data.librarySoundUseFailure
import com.us.android.core.media.SoundEntry
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.media.sound.SoundPreviewPlayer
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The Library list's preview: one player for every row, by library id.
 * An interface so the list's rules are tested without a player; the app
 * binds [PlayerLibrarySoundPreview].
 */
interface LibrarySoundPreview {
    /** What the playing row's button shows. */
    val state: StateFlow<SoundPreview>

    /** Play ↔ pause [libraryId]; another row replaces the one playing. */
    fun toggle(libraryId: String)

    /** The surface left the foreground. */
    fun pause()

    /** Nothing plays, and nothing is remembered. */
    fun stop()

    fun release()
}

/** [LibrarySoundPreview] on the shared preview player: `v1/audio-library/{id}/serve`. */
class PlayerLibrarySoundPreview(private val player: SoundPreviewPlayer) : LibrarySoundPreview {
    override val state: StateFlow<SoundPreview> = player.state
    override fun toggle(libraryId: String) = player.toggleLibrary(libraryId)
    override fun pause() = player.pause()
    override fun stop() = player.stop()
    override fun release() = player.release()
}

/** The Library list, as it is drawn. */
data class LibrarySoundsUiState(
    /** The chosen genre; "" is every genre. */
    val genre: String = "",
    /** Every genre seen so far, so the filter keeps its options once one is chosen. */
    val genres: List<String> = emptyList(),
    val loading: Boolean = true,
    /** The first page could not be read. */
    val error: String? = null,
    val sounds: List<LibrarySound> = emptyList(),
    val nextOffset: Int? = null,
    val loadingMore: Boolean = false,
    /** Only the NEXT page failed: the rows already shown stay. */
    val moreFailed: Boolean = false,
    /** The library id the preview button belongs to; every other row shows Play. */
    val previewingId: String? = null,
    /** The library id whose "Use" is in flight; every Use waits while one is. */
    val usingId: String? = null,
    /** Why the last "Use" failed, in the list's own words. */
    val useMessage: String? = null,
    /**
     * The `audio_tracks` id a "Use" chose, until the screen has acted on it:
     * the sound is already waiting in [SoundEntry].
     */
    val chosenSoundId: String? = null,
) {
    val hasMore: Boolean get() = nextOffset != null
}

/**
 * The curated sound library (2026-10-15), shared by the reel form's Library
 * sheet and the Creator Hub's Sounds tab: browse by genre, preview a row on
 * press — never on its own — and "Use" it.
 *
 * "Use" asks media-service for the `audio_tracks` id the row plays as and
 * leaves THAT sound in [SoundEntry], the one door every "Use this sound"
 * takes into the reel create flow; the form takes it from there exactly as
 * it takes a sound chosen in Reels. The library id never leaves this list.
 */
@HiltViewModel
class LibrarySoundsViewModel @Inject constructor(
    private val repository: LibrarySoundsRepository,
    private val soundEntry: SoundEntry,
    private val preview: LibrarySoundPreview,
) : ViewModel() {

    private val _state = MutableStateFlow(LibrarySoundsUiState())
    val state: StateFlow<LibrarySoundsUiState> = _state.asStateFlow()

    /** What the playing row's button shows. */
    val previewState: StateFlow<SoundPreview> = preview.state

    private var pageJob: Job? = null

    init {
        load(more = false)
    }

    /** A genre pill; the same one again changes nothing. */
    fun onGenre(genre: String) {
        if (genre == _state.value.genre && _state.value.error == null) return
        stopPreview()
        _state.update { it.copy(genre = genre, sounds = emptyList(), nextOffset = null, useMessage = null) }
        load(more = false)
    }

    /** Try again (first page) or Load more. */
    fun load(more: Boolean) {
        val current = _state.value
        if (more && (current.nextOffset == null || current.loadingMore || current.loading)) return
        if (!more) pageJob?.cancel()
        val genre = current.genre
        val offset = if (more) current.nextOffset ?: 0 else 0
        _state.update {
            if (more) it.copy(loadingMore = true, moreFailed = false) else it.copy(loading = true, error = null)
        }
        pageJob = viewModelScope.launch {
            val result = repository.page(genre, offset)
            _state.update { s ->
                if (s.genre != genre) return@update s
                when (result) {
                    is AppResult.Success -> s.copy(
                        loading = false,
                        loadingMore = false,
                        error = null,
                        moreFailed = false,
                        sounds = if (more) (s.sounds + result.data.sounds).distinctBy { it.id } else result.data.sounds,
                        nextOffset = result.data.nextOffset,
                        genres = libraryGenres(s.genres, result.data.sounds),
                    )
                    is AppResult.Failure ->
                        if (more) s.copy(loadingMore = false, moreFailed = true) else s.copy(loading = false, error = LIBRARY_UNAVAILABLE)
                }
            }
        }
    }

    /** The row's play button: press to play, press again to pause. A row with nothing to play does nothing. */
    fun togglePreview(sound: LibrarySound) {
        if (!sound.playable) return
        _state.update { it.copy(previewingId = sound.id) }
        preview.toggle(sound.id)
    }

    /** The list closed or a choice was made: nothing plays. */
    fun stopPreview() {
        preview.stop()
        _state.update { it.copy(previewingId = null) }
    }

    /** The surface left the foreground: the preview pauses where it is. */
    fun pausePreview() = preview.pause()

    /** "Use": the sound goes to [SoundEntry] by its `audio_tracks` id; a refusal is said in the list. */
    fun use(sound: LibrarySound) {
        if (_state.value.usingId != null) return
        stopPreview()
        _state.update { it.copy(usingId = sound.id, useMessage = null) }
        viewModelScope.launch {
            when (val result = repository.use(sound)) {
                is AppResult.Success -> {
                    soundEntry.choose(result.data)
                    _state.update { it.copy(usingId = null, chosenSoundId = result.data.id) }
                }
                is AppResult.Failure -> _state.update {
                    it.copy(usingId = null, useMessage = result.error.librarySoundUseFailure().message)
                }
            }
        }
    }

    /** The screen has opened the create flow (or the form has taken the sound). */
    fun onChosenHandled() = _state.update { it.copy(chosenSoundId = null) }

    fun dismissUseMessage() = _state.update { it.copy(useMessage = null) }

    override fun onCleared() = preview.release()
}
