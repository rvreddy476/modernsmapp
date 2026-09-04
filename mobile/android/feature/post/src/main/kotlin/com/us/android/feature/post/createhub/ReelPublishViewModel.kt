package com.us.android.feature.post.createhub

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.media.publish.VideoKind
import com.us.android.feature.post.data.dto.SupportedAudience
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.UUID
import javax.inject.Inject

/**
 * The Create hub's Reel AND Video surface: pick a video, describe it, choose
 * a cover, set who sees it and what viewers may do with it, post it — as a
 * `flick` (a reel) or a `long_video` (Tube), by [ReelUiState.kind].
 *
 * ## THE FORM (founder, 2026-09-04; Video added 2026-09-05)
 *
 * TikTok-shaped. A reel has no title: the description is the only text, and
 * the server extracts hashtags from it, so the client sends text and nothing
 * derived from it. A LONG video adds a required title (100 characters) above
 * the description, a 16:9 cover strip in place of the 9:16 one, and no remix
 * switch. Tag people, a typed place name, Audience, Category, and the
 * switches sent EXPLICITLY on every post — an omitted `allow_download` is
 * "unspecified", not "true".
 *
 * ## THE GATE
 *
 * A picked video is probed for its length and size before anything else
 * ([ReelVideoProbe]); [videoGate] decides. A reel over five minutes is not
 * refused: the form says so and offers [switchToLong], which keeps the
 * selection, the cover and every field and only changes what the post will
 * be. A file over 500 MB is refused for either kind.
 *
 * ## POST HANDS OFF AND LEAVES
 *
 * Tapping Post no longer uploads here. A phone video took the dev server
 * seventeen minutes to transcode (2026-09-04) and the founder watched a
 * spinner the whole time, so the form now writes the chosen cover to disk,
 * hands a [PendingReelPublish] to the [ReelPublishLauncher] — WorkManager
 * from there, see [ReelPublishWorker] — and reports [Phase.Enqueued] so the
 * surface can close. The Reels tab's (or Tube's) pending item shows the rest.
 */
@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ReelPublishViewModel @Inject constructor(
    private val launcher: ReelPublishLauncher,
    private val files: ReelPublishFiles,
    private val encoder: ReelCoverEncoder,
    private val frames: ReelFrameExtractor,
    private val lookups: ReelLookups,
    private val probe: ReelVideoProbe,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    sealed interface Phase {
        data object Editing : Phase

        /** The cover is being written and the publish handed over — a moment. */
        data object Preparing : Phase

        /** Handed to the worker; the surface closes on this. */
        data object Enqueued : Phase
        data class Failure(val message: String) : Phase
    }

    data class ReelUiState(
        /** Reel or long video — what the form is making. Switchable by the gate. */
        val kind: VideoKind = VideoKind.REEL,
        val videoUri: String? = null,
        /** What the probe found; null until it answers, and for an unreadable file. */
        val probe: VideoProbe? = null,
        /** The long video's title; ignored for a reel. */
        val title: String = "",
        val caption: String = "",
        val frames: List<CoverFrame> = emptyList(),
        val framesLoading: Boolean = false,
        val coverIndex: Int = 0,
        val visibility: String = VISIBILITY_PUBLIC,
        val category: String = "",
        val categories: List<ReelCategory> = FallbackReelCategories,
        val allowComments: Boolean = true,
        val hideShare: Boolean = false,
        val allowDownload: Boolean = true,
        val allowRemix: Boolean = true,
        val taggedUsers: List<TaggedUser> = emptyList(),
        val peopleQuery: String = "",
        val peopleResults: List<TaggedUser> = emptyList(),
        val peopleSearching: Boolean = false,
        val locationName: String = "",
        val phase: Phase = Phase.Editing,
    ) {
        /** The chosen cover, or null when no frame could be extracted. */
        val cover: CoverFrame?
            get() = frames.getOrNull(coverIndex)?.takeIf { it.bitmap != null }

        val isBusy: Boolean
            get() = phase is Phase.Preparing || phase is Phase.Enqueued

        /** Whether the picked file may be posted as [kind] — see [videoGate]. */
        val gate: VideoGate
            get() = videoGate(kind, probe)

        /** A long video needs its title; a reel needs nothing but the video. */
        val hasRequiredText: Boolean
            get() = kind == VideoKind.REEL || title.isNotBlank()

        /** A media-only post is legal, so a chosen video that passes the gate is enough. */
        val canPost: Boolean
            get() = videoUri != null && gate.allowsPost && hasRequiredText &&
                (phase is Phase.Editing || phase is Phase.Failure)

        val canTagMore: Boolean
            get() = taggedUsers.size < MAX_TAGGED_PEOPLE
    }

    private val _state = MutableStateFlow(
        ReelUiState(kind = videoKindForSurface(savedStateHandle[SURFACE_ARG])),
    )
    val state: StateFlow<ReelUiState> = _state.asStateFlow()

    private var creationKey: String = UUID.randomUUID().toString()

    private var framesJob: Job? = null
    private var probeJob: Job? = null
    private var peopleJob: Job? = null

    init {
        viewModelScope.launch {
            lookups.categories()?.let { loaded -> _state.update { it.copy(categories = loaded) } }
        }
    }

    // ── Video and cover ─────────────────────────────────────────────────

    fun onVideoPicked(uri: String) {
        // A different video is a different post: new key, new frames, new probe.
        creationKey = UUID.randomUUID().toString()
        _state.update {
            it.copy(
                videoUri = uri,
                probe = null,
                frames = emptyList(),
                framesLoading = true,
                coverIndex = 0,
                phase = Phase.Editing,
            )
        }
        framesJob?.cancel()
        framesJob = viewModelScope.launch {
            val extracted = frames.extract(uri, COVER_FRAME_COUNT)
            _state.update { current ->
                if (current.videoUri == uri) current.copy(frames = extracted, framesLoading = false) else current
            }
        }
        probeJob?.cancel()
        probeJob = viewModelScope.launch {
            val found = probe.probe(uri)
            _state.update { current -> if (current.videoUri == uri) current.copy(probe = found) else current }
        }
    }

    fun onCoverSelected(index: Int) = _state.update {
        if (index in it.frames.indices) it.copy(coverIndex = index) else it
    }

    /**
     * The gate's way out for a reel over five minutes: the same video, the
     * same cover and every field, posted as a long video instead. The reel's
     * remix switch simply stops applying.
     */
    fun switchToLong() = _state.update { it.copy(kind = VideoKind.LONG) }

    // ── Fields ──────────────────────────────────────────────────────────

    /** Clamped to [MAX_TITLE_LENGTH]; the field shows the counter. */
    fun onTitleChanged(value: String) = _state.update { it.copy(title = value.take(MAX_TITLE_LENGTH)) }

    fun onCaptionChanged(value: String) = _state.update { it.copy(caption = value) }

    fun onVisibilityChanged(value: String) = _state.update {
        if (value in SupportedAudience) it.copy(visibility = value) else it
    }

    /** An empty id is "None". */
    fun onCategoryChanged(id: String) = _state.update { it.copy(category = id) }

    fun onAllowCommentsChanged(on: Boolean) = _state.update { it.copy(allowComments = on) }

    fun onHideShareChanged(on: Boolean) = _state.update { it.copy(hideShare = on) }

    fun onAllowDownloadChanged(on: Boolean) = _state.update { it.copy(allowDownload = on) }

    fun onAllowRemixChanged(on: Boolean) = _state.update { it.copy(allowRemix = on) }

    fun onLocationChanged(name: String) = _state.update { it.copy(locationName = name) }

    // ── Tag people ──────────────────────────────────────────────────────

    fun onPeopleQueryChanged(query: String) {
        _state.update { it.copy(peopleQuery = query) }
        peopleJob?.cancel()
        val trimmed = query.trim()
        if (trimmed.length < MIN_PEOPLE_QUERY) {
            _state.update { it.copy(peopleResults = emptyList(), peopleSearching = false) }
            return
        }
        _state.update { it.copy(peopleSearching = true) }
        peopleJob = viewModelScope.launch {
            delay(PEOPLE_DEBOUNCE_MILLIS)
            val results = lookups.searchPeople(trimmed)
            _state.update { it.copy(peopleResults = results, peopleSearching = false) }
        }
    }

    fun onTagUser(user: TaggedUser) = _state.update {
        when {
            it.taggedUsers.any { tagged -> tagged.id == user.id } -> it
            !it.canTagMore -> it
            else -> it.copy(taggedUsers = it.taggedUsers + user)
        }
    }

    fun onUntagUser(userId: String) = _state.update {
        it.copy(taggedUsers = it.taggedUsers.filterNot { tagged -> tagged.id == userId })
    }

    // ── Post ────────────────────────────────────────────────────────────

    fun onPost() {
        val current = _state.value
        if (!current.canPost) return
        if (launcher.isBusy) {
            fail("Your last video is still posting. Try again when it's done.")
            return
        }
        _state.update { it.copy(phase = Phase.Preparing) }
        viewModelScope.launch {
            val coverPath = current.cover?.let { frame ->
                withContext(io) { encoder.encode(frame)?.let { bytes -> files.writeCover(bytes, creationKey) } }
            }
            // A cover the user chose that cannot be prepared stops the post:
            // posting without it would publish something they did not approve.
            if (current.cover != null && coverPath == null) {
                fail("That cover frame couldn't be prepared. Pick another.")
                return@launch
            }
            launcher.enqueue(buildPending(current, creationKey, coverPath))
            _state.update { it.copy(phase = Phase.Enqueued) }
        }
    }

    private fun fail(message: String) = _state.update { it.copy(phase = Phase.Failure(message)) }

    companion object {
        /** The long video's title cap — the server's, mirrored so the counter is honest. */
        const val MAX_TITLE_LENGTH = 100

        /** The Create route's argument, read to open the form as a reel or a video. */
        const val SURFACE_ARG = "surface"

        private const val COVER_FRAME_COUNT = 6
        private const val MIN_PEOPLE_QUERY = 2
        private const val PEOPLE_DEBOUNCE_MILLIS = 300L

        /** The Video tile opens the form as a long video; every other way in is a reel. */
        fun videoKindForSurface(routeKey: String?): VideoKind =
            if (routeKey == CreateSurface.Video.routeKey) VideoKind.LONG else VideoKind.REEL

        /** The form, whole, as the record the worker publishes from. */
        fun buildPending(current: ReelUiState, creationKey: String, coverPath: String?) = PendingReelPublish(
            creationKey = creationKey,
            videoUri = current.videoUri.orEmpty(),
            coverPath = coverPath,
            kind = current.kind,
            title = current.title,
            caption = current.caption,
            visibility = current.visibility,
            category = current.category,
            allowComments = current.allowComments,
            hideShare = current.hideShare,
            allowDownload = current.allowDownload,
            allowRemix = current.allowRemix,
            taggedUserIds = current.taggedUsers.map { it.id },
            locationName = current.locationName,
        )
    }
}
