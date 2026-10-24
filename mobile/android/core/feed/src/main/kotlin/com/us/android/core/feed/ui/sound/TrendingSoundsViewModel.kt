package com.us.android.core.feed.ui.sound

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.TRENDING_UNAVAILABLE
import com.us.android.core.feed.data.TrendingFilter
import com.us.android.core.feed.data.TrendingSound
import com.us.android.core.feed.data.TrendingSoundsRepository
import com.us.android.core.feed.data.appendTrending
import com.us.android.core.feed.data.toChosenSound
import com.us.android.core.feed.data.trendingShelf
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
 * The Trending list's preview: one player for every row, by the sound's
 * `audio_tracks` id (`v1/audio/{id}/serve`). An interface so the list's
 * rules are tested without a player; the app binds [PlayerTrendingSoundPreview].
 */
interface TrendingSoundPreview {
    val state: StateFlow<SoundPreview>
    fun toggle(soundId: String)
    fun pause()
    fun stop()
    fun release()
}

/** [TrendingSoundPreview] on the shared preview player. */
class PlayerTrendingSoundPreview(private val player: SoundPreviewPlayer) : TrendingSoundPreview {
    override val state: StateFlow<SoundPreview> = player.state
    override fun toggle(soundId: String) = player.toggle(soundId)
    override fun pause() = player.pause()
    override fun stop() = player.stop()
    override fun release() = player.release()
}

/** One tab of the list, read a page at a time. */
data class TrendingTab(
    val sounds: List<TrendingSound> = emptyList(),
    /** The first page is on its way. */
    val loading: Boolean = false,
    /** A first page has been answered (rows or none): the tab is not read again on a switch. */
    val loaded: Boolean = false,
    /** The first page could not be read. */
    val error: String? = null,
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    /** Only the NEXT page failed: the rows already shown stay. */
    val moreFailed: Boolean = false,
) {
    val hasMore: Boolean get() = nextCursor != null

    /** A next page exists and nothing is on its way. */
    val canLoadMore: Boolean get() = nextCursor != null && !loadingMore && !loading
}

/** The Trending list, as it is drawn: the shelf over the picker and the full list with its two tabs. */
data class TrendingSoundsUiState(
    val filter: TrendingFilter = TrendingFilter.ALL,
    val all: TrendingTab = TrendingTab(loading = true),
    val rising: TrendingTab = TrendingTab(),
    /** The sound id the preview button belongs to; every other row shows Play. */
    val previewingId: String? = null,
    /** The sound a "Use" chose, until the screen has acted on it: it is already in [SoundEntry]. */
    val chosenSoundId: String? = null,
) {
    /** The tab on screen. */
    val current: TrendingTab get() = tab(filter)

    /** The picker's shelf: the top of All, whichever tab the full list was left on. */
    val shelf: List<TrendingSound> get() = trendingShelf(all.sounds)

    fun tab(filter: TrendingFilter): TrendingTab = when (filter) {
        TrendingFilter.ALL -> all
        TrendingFilter.RISING -> rising
    }

    fun withTab(filter: TrendingFilter, tab: TrendingTab): TrendingSoundsUiState = when (filter) {
        TrendingFilter.ALL -> copy(all = tab)
        TrendingFilter.RISING -> copy(rising = tab)
    }
}

/**
 * Trending sounds (2026-10-24): the shelf at the top of the sound picker and
 * the full list (All / Rising) behind it, and the screen pushed from a
 * sound's page. One ViewModel for all three, so the shelf and the list are
 * one reading of the ranking and one player.
 *
 * Nothing plays until a preview button is pressed. "Use this sound" needs no
 * server call: a ranked sound is already an `audio_tracks` row, so it goes
 * straight into [SoundEntry] — the one door every "Use this sound" takes
 * into the reel create flow.
 */
@HiltViewModel
class TrendingSoundsViewModel @Inject constructor(
    private val repository: TrendingSoundsRepository,
    private val soundEntry: SoundEntry,
    private val preview: TrendingSoundPreview,
) : ViewModel() {

    private val _state = MutableStateFlow(TrendingSoundsUiState())
    val state: StateFlow<TrendingSoundsUiState> = _state.asStateFlow()

    /** What the playing row's button shows. */
    val previewState: StateFlow<SoundPreview> = preview.state

    private val jobs = mutableMapOf<TrendingFilter, Job>()

    init {
        load(TrendingFilter.ALL, more = false)
    }

    /** A tab. The first visit to Rising reads it; after that a switch reads nothing. */
    fun onFilter(filter: TrendingFilter) {
        if (filter == _state.value.filter) return
        stopPreview()
        _state.update { it.copy(filter = filter) }
        val tab = _state.value.tab(filter)
        if (!tab.loaded && !tab.loading) load(filter, more = false)
    }

    /** Try again (first page) or Load more, on the tab on screen. */
    fun load(more: Boolean) = load(_state.value.filter, more)

    private fun load(filter: TrendingFilter, more: Boolean) {
        val tab = _state.value.tab(filter)
        if (more && !tab.canLoadMore) return
        if (!more) jobs[filter]?.cancel()
        val cursor = if (more) tab.nextCursor else null
        _state.update {
            it.withTab(
                filter,
                if (more) tab.copy(loadingMore = true, moreFailed = false) else tab.copy(loading = true, error = null),
            )
        }
        jobs[filter] = viewModelScope.launch {
            val result = repository.page(filter, cursor)
            _state.update { s ->
                val now = s.tab(filter)
                val next = when (result) {
                    is AppResult.Success -> now.copy(
                        sounds = if (more) appendTrending(now.sounds, result.data.sounds) else result.data.sounds,
                        nextCursor = result.data.nextCursor,
                        loading = false,
                        loaded = true,
                        loadingMore = false,
                        moreFailed = false,
                        error = null,
                    )
                    is AppResult.Failure ->
                        if (more) {
                            now.copy(loadingMore = false, moreFailed = true)
                        } else {
                            now.copy(loading = false, error = TRENDING_UNAVAILABLE)
                        }
                }
                s.withTab(filter, next)
            }
        }
    }

    /** A row's play button: press to play, press again to pause; another row moves the one player. */
    fun togglePreview(sound: TrendingSound) {
        _state.update { it.copy(previewingId = sound.sound.id) }
        preview.toggle(sound.sound.id)
    }

    /** The list closed, a tab changed or a choice was made: nothing plays. */
    fun stopPreview() {
        preview.stop()
        _state.update { it.copy(previewingId = null) }
    }

    /** The surface left the foreground: the preview pauses where it is. */
    fun pausePreview() = preview.pause()

    /** "Use this sound": the sound goes to [SoundEntry] by its `audio_tracks` id. */
    fun use(sound: TrendingSound) {
        stopPreview()
        soundEntry.choose(sound.toChosenSound())
        _state.update { it.copy(chosenSoundId = sound.sound.id) }
    }

    /** The screen has opened the create flow (or the form has taken the sound). */
    fun onChosenHandled() = _state.update { it.copy(chosenSoundId = null) }

    override fun onCleared() = preview.release()
}
