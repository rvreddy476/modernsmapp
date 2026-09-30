package com.us.android.feature.feed.ui.reels.sound

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.SOUND_GONE_MESSAGE
import com.us.android.core.feed.data.SoundReelTile
import com.us.android.core.feed.data.SoundReelsPage
import com.us.android.core.feed.data.SoundsRepository
import com.us.android.core.feed.data.soundReelTiles
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.SoundEntry
import com.us.android.core.model.FeedItem
import com.us.android.core.model.ReelSound
import com.us.android.feature.feed.ui.reels.toChosenSound
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where the sound page stands. */
sealed interface SoundPagePhase {
    /** The first page is on its way. */
    data object Loading : SoundPagePhase

    /** The sound and its first page are here. */
    data object Ready : SoundPagePhase

    /** The sound is gone, or not this viewer's to hear: the server gives one answer for both, and so does the page. */
    data object Gone : SoundPagePhase

    /** The first page could not be loaded; Retry asks again. */
    data class Failed(val message: String) : SoundPagePhase
}

/**
 * The sound page, as one value.
 *
 * @property sound the sound itself, from the first page that carried it.
 * @property tiles every page's reels in order, the origin first and marked,
 *   nothing twice — [soundReelTiles].
 * @property canLoadMore the last page named a next one.
 * @property loadingMore a later page is on its way.
 * @property moreFailed the last ask for a later page failed; the tiles stay,
 *   and the ask is made again when the grid reaches its end again or Retry
 *   is tapped.
 */
data class SoundPageState(
    val phase: SoundPagePhase = SoundPagePhase.Loading,
    val sound: ReelSound? = null,
    val tiles: List<SoundReelTile> = emptyList(),
    val canLoadMore: Boolean = false,
    val loadingMore: Boolean = false,
    val moreFailed: Boolean = false,
) {
    /** The sound exists and no reel plays it yet — not even its origin is readable. */
    val isEmpty: Boolean get() = phase == SoundPagePhase.Ready && tiles.isEmpty()

    /** The grid reaching its end should ask for the next page: there is one, and none is on its way or just failed. */
    val wantsMore: Boolean get() = canLoadMore && !loadingMore && !moreFailed
}

/**
 * The pages so far as the page's state. Pure: the screen's whole reading of
 * the server's answers, without a server.
 */
fun soundPageState(pages: List<SoundReelsPage>, loadingMore: Boolean = false, moreFailed: Boolean = false) =
    SoundPageState(
        phase = SoundPagePhase.Ready,
        sound = pages.firstNotNullOfOrNull { it.sound },
        tiles = soundReelTiles(pages),
        canLoadMore = pages.lastOrNull()?.nextCursor != null,
        loadingMore = loadingMore,
        moreFailed = moreFailed,
    )

/** What the first page's failure means for the page: gone, or worth another try. */
fun soundPagePhase(error: AppError): SoundPagePhase = when (error) {
    is AppError.NotFound -> SoundPagePhase.Gone
    is AppError.NoNetwork -> SoundPagePhase.Failed("You're offline. Check your connection and try again.")
    is AppError.Timeout -> SoundPagePhase.Failed("That took too long. Try again.")
    else -> SoundPagePhase.Failed("We couldn't load this sound.")
}

/**
 * A sound's page (original sounds, 2026-09-30): what it is called, who made
 * it, how many reels play it, the reel it was taken from, and the reels that
 * play it, newest first, a page at a time.
 *
 * `GET v1/posts/by-sound/{id}` answers all of it: the sound rides on every
 * page and the origin on the first. A sound that is gone, or whose source
 * video this viewer may not watch, is a 404 — one answer, shown as
 * [SOUND_GONE_MESSAGE].
 *
 * "Use this sound" leaves the sound in [SoundEntry] for the reel create
 * flow; a tile leaves its reel's id in [ReelsEntry] for the Reels tab.
 * Neither navigates: that is the screen's, through `:app`.
 */
@HiltViewModel
class SoundPageViewModel @Inject constructor(
    private val sounds: SoundsRepository,
    private val urlResolver: MediaUrlResolver,
    private val soundEntry: SoundEntry,
    private val reelsEntry: ReelsEntry,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    val soundId: String = savedStateHandle.get<String>(SOUND_ID_ARG).orEmpty()

    private val pages = mutableListOf<SoundReelsPage>()
    private var loading: Job? = null

    private val _state = MutableStateFlow(SoundPageState())
    val state: StateFlow<SoundPageState> = _state.asStateFlow()

    init {
        load()
    }

    /** The first page, again from the top. Also what Retry does. */
    fun load() {
        loading?.cancel()
        pages.clear()
        _state.value = SoundPageState(phase = SoundPagePhase.Loading)
        loading = viewModelScope.launch {
            when (val result = sounds.reels(soundId)) {
                is AppResult.Success -> {
                    pages += result.data
                    _state.value = soundPageState(pages)
                }
                is AppResult.Failure -> _state.value = SoundPageState(phase = soundPagePhase(result.error))
            }
        }
    }

    /** The grid reached its end: the next page, when there is one and none is already on its way. */
    fun loadMore() {
        val cursor = pages.lastOrNull()?.nextCursor ?: return
        if (loading?.isActive == true) return
        _state.update { it.copy(loadingMore = true, moreFailed = false) }
        loading = viewModelScope.launch {
            when (val result = sounds.reels(soundId, cursor)) {
                is AppResult.Success -> {
                    pages += result.data
                    _state.value = soundPageState(pages)
                }
                // The tiles already shown stay: a later page failing is not the page failing.
                is AppResult.Failure -> _state.update { it.copy(loadingMore = false, moreFailed = true) }
            }
        }
    }

    /** "Use this sound": the reel create flow will open with it chosen. False when there is no sound to take. */
    fun chooseSound(): Boolean {
        val sound = _state.value.sound ?: return false
        soundEntry.choose(sound.toChosenSound())
        return true
    }

    /** A tile was tapped: Reels will open on that reel. */
    fun openReel(postId: String) = reelsEntry.open(postId)

    /** The still a tile draws: the cover the author chose, else the transcode's own. */
    fun posterUrl(item: FeedItem): String? = urlResolver.videoThumb(item).url

    companion object {
        /** The route's argument: the sound's id. */
        const val SOUND_ID_ARG = "soundId"
    }
}
