package com.us.android.feature.post.createhub

import android.graphics.Bitmap
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.media.publish.ScheduleWindow
import com.us.android.core.media.publish.VideoKind
import com.us.android.feature.post.data.dto.SupportedAudience
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.time.Instant
import java.util.UUID
import javax.inject.Inject

/**
 * The Create hub's Reel AND Video details step: describe the video, choose
 * a cover, set who sees it and what viewers may do with it, post it — as a
 * `flick` (a reel) or a `long_video` (Tube), by [ReelUiState.kind].
 *
 * ## THE FORM (founder, 2026-09-04; Video 2026-09-05; the studio 2026-09-05)
 *
 * TikTok-shaped. A reel has no title: the caption is the only text — and
 * since the studio, the caption is JUST the caption: hashtags are chips in
 * a field of their own ([Hashtags]) and mentions are the people picked, so
 * the server gets `hashtags` and `mentions` as arrays, never parsed out of
 * prose. A LONG video adds a required title (100 characters) above the
 * description, a 16:9 cover in place of the 9:16 one, and no remix switch.
 * Tag people, a typed place name, Audience, Category, and the switches sent
 * EXPLICITLY on every post — an omitted `allow_download` is "unspecified",
 * not "true".
 *
 * ## THE COVER (founder, 2026-09-05: an exact frame)
 *
 * The picker shows the chosen frame large over a filmstrip of two dozen
 * thumbnails; dragging the handle asks [ReelFrameSeeker] for the frame at
 * the handle's exact instant — throttled to one in flight, the newest
 * request winning — and Confirm makes that frame the cover. "Upload" takes
 * a gallery image through [ReelCoverImageLoader] instead. Either way the
 * cover is a [CoverFrame] with a bitmap the encoder turns into the JPEG. A
 * reel's frames come from the studio's EXPORT, so the cover is a frame of
 * what will actually be posted.
 *
 * ## THE GATE
 *
 * A picked video is probed for its length and size before anything else
 * ([ReelVideoProbe]); [videoGate] decides. A reel over five minutes is not
 * refused: the form says so and offers [switchToLong], which keeps the
 * selection, the cover and every field and only changes what the post will
 * be. A file over 500 MB is refused for either kind.
 *
 * ## SCHEDULE
 *
 * Beside Post sits Schedule: a date and time five minutes to thirty days
 * ahead ([ScheduleWindow]), carried as `publish_at`. The server holds the
 * post and answers `is_scheduled`; the own profile shows it with a clock
 * until then.
 *
 * ## POST HANDS OFF AND LEAVES
 *
 * Tapping Post no longer uploads here. The form writes the chosen cover to
 * disk, hands a [PendingReelPublish] to the [ReelPublishLauncher] —
 * WorkManager from there, see [ReelPublishWorker] — and reports
 * [Phase.Enqueued] so the surface can close onto the own profile, whose
 * grid shows the tile. Several may be pending at once (2026-09-05): the
 * queue uploads them in order.
 */
@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList", "TooManyFunctions")
@OptIn(ExperimentalCoroutinesApi::class, FlowPreview::class)
class ReelPublishViewModel @Inject constructor(
    private val launcher: ReelPublishLauncher,
    private val files: ReelPublishFiles,
    private val encoder: ReelCoverEncoder,
    private val frames: ReelFrameExtractor,
    private val seeker: ReelFrameSeeker,
    private val images: ReelCoverImageLoader,
    private val lookups: ReelLookups,
    private val probe: ReelVideoProbe,
    private val channels: ChannelRepository,
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

    /** Where the cover came from — the video, or the gallery. */
    enum class CoverSource { Frame, Upload }

    /**
     * The picker while it is open: the handle's instant, the frame found
     * there (null until the seek answers), and whether a seek is running.
     */
    data class CoverPicker(val timeUs: Long, val preview: Bitmap?, val seeking: Boolean)

    data class ReelUiState(
        /** Reel or long video — what the form is making. Switchable by the gate. */
        val kind: VideoKind = VideoKind.REEL,
        val videoUri: String? = null,
        /** The studio's exported file, when the video came through it: uploaded as is, never copied. */
        val exportedPath: String? = null,
        /** What the probe found; null until it answers, and for an unreadable file. */
        val probe: VideoProbe? = null,
        /** The long video's title; ignored for a reel. */
        val title: String = "",
        val caption: String = "",
        /** The hashtag chips, without `#`. */
        val hashtags: List<String> = emptyList(),
        /** What is being typed into the hashtag field, before it becomes a chip. */
        val hashtagInput: String = "",
        val hashtagSuggestions: List<String> = emptyList(),
        /** The filmstrip's thumbnails. */
        val frames: List<CoverFrame> = emptyList(),
        val framesLoading: Boolean = false,
        /** The chosen cover, or null when no frame could be extracted and nothing was uploaded. */
        val cover: CoverFrame? = null,
        val coverSource: CoverSource = CoverSource.Frame,
        /** Non-null while the cover picker is open. */
        val picker: CoverPicker? = null,
        val visibility: String = VISIBILITY_PUBLIC,
        val category: String = "",
        val categories: List<ReelCategory> = FallbackReelCategories,
        val allowComments: Boolean = true,
        val hideShare: Boolean = false,
        val allowDownload: Boolean = true,
        val allowRemix: Boolean = true,
        /** The people mentioned — chips of `@username`, sent as both ids and usernames. */
        val taggedUsers: List<TaggedUser> = emptyList(),
        val peopleQuery: String = "",
        val peopleResults: List<TaggedUser> = emptyList(),
        val peopleSearching: Boolean = false,
        val locationName: String = "",
        /** When the post goes live, or null to post now. */
        val publishAt: Instant? = null,
        val phase: Phase = Phase.Editing,
        /** The viewer's channel, asked for a long video; unknown for a reel. */
        val channel: ChannelState = ChannelState.Unknown,
    ) {
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

        val canAddHashtags: Boolean
            get() = hashtags.size < Hashtags.MAX_HASHTAGS

        /** The video's length in microseconds: the probe's, else inferred from the strip's spacing. */
        val durationUs: Long
            get() = probe?.durationMs?.takeIf { it > 0L }?.times(MICROS_PER_MILLI)
                ?: frames.takeIf { it.size > 1 }?.let { it.last().timeUs * it.size / (it.size - 1) }
                ?: 0L

        /** The cover's aspect (width / height): 16:9 for a video, 9:16 for a reel. */
        val coverAspect: Float
            get() = if (kind == VideoKind.LONG) LANDSCAPE_ASPECT else PORTRAIT_ASPECT
    }

    private val _state = MutableStateFlow(
        ReelUiState(kind = videoKindForSurface(savedStateHandle[SURFACE_ARG])),
    )
    val state: StateFlow<ReelUiState> = _state.asStateFlow()

    private var creationKey: String = UUID.randomUUID().toString()

    /** Where the studio's export lands for this post: the worker uploads it from there without a copy. */
    fun exportTargetPath(): String = files.exportTarget(creationKey)

    private var framesJob: Job? = null
    private var probeJob: Job? = null
    private var coverJob: Job? = null
    private var peopleJob: Job? = null
    private var hashtagJob: Job? = null

    /** The handle's newest instant; the seek loop takes the latest and drops the rest. */
    private val scrubs = MutableSharedFlow<Long>(extraBufferCapacity = 1)

    init {
        viewModelScope.launch {
            lookups.categories()?.let { loaded -> _state.update { it.copy(categories = loaded) } }
        }
        viewModelScope.launch { channels.own.collect { known -> _state.update { it.copy(channel = known) } } }
        if (_state.value.kind == VideoKind.LONG) ensureChannel()
        viewModelScope.launch { scrubs.collectLatest { timeUs -> seekPreview(timeUs) } }
    }

    // ── Channel ─────────────────────────────────────────────────────────

    /** Asks for the viewer's channel once (a long video needs one); a failed read is retried by [retryChannel]. */
    fun ensureChannel() {
        viewModelScope.launch { channels.ensureLoaded() }
    }

    fun retryChannel() {
        viewModelScope.launch { channels.refresh() }
    }

    // ── Video and cover ─────────────────────────────────────────────────

    /** A video picked as is (a long video, or a reel before the studio existed). */
    fun onVideoPicked(uri: String) = startWith(uri, exportedPath = null, newKey = true)

    /**
     * The studio's export for the CURRENT key (it wrote to
     * [ReelPublishFiles.exportTarget] under it): the frames, the probe and
     * the cover come from this file, and the worker uploads it without a copy.
     */
    fun onReelExported(path: String) = startWith(File(path).toURI().toString(), exportedPath = path, newKey = false)

    /** "Change video": back to the source step, forgetting the video and its cover but keeping the fields. */
    fun clearVideo() {
        framesJob?.cancel()
        probeJob?.cancel()
        coverJob?.cancel()
        creationKey = UUID.randomUUID().toString()
        _state.update {
            it.copy(
                videoUri = null,
                exportedPath = null,
                probe = null,
                frames = emptyList(),
                framesLoading = false,
                cover = null,
                coverSource = CoverSource.Frame,
                picker = null,
                phase = Phase.Editing,
            )
        }
    }

    private fun startWith(uri: String, exportedPath: String?, newKey: Boolean) {
        // A different video is a different post: new key, new frames, new probe, new cover.
        if (newKey) creationKey = UUID.randomUUID().toString()
        _state.update {
            it.copy(
                videoUri = uri,
                exportedPath = exportedPath,
                probe = null,
                frames = emptyList(),
                framesLoading = true,
                cover = null,
                coverSource = CoverSource.Frame,
                picker = null,
                phase = Phase.Editing,
            )
        }
        framesJob?.cancel()
        framesJob = viewModelScope.launch {
            val extracted = frames.extract(uri, Filmstrip.FRAME_COUNT)
            _state.update { current ->
                if (current.videoUri == uri) current.copy(frames = extracted, framesLoading = false) else current
            }
        }
        probeJob?.cancel()
        probeJob = viewModelScope.launch {
            val found = probe.probe(uri)
            _state.update { current -> if (current.videoUri == uri) current.copy(probe = found) else current }
        }
        coverJob?.cancel()
        coverJob = viewModelScope.launch {
            // The default cover: the very first frame, at full size. Falls
            // back to the strip's first thumbnail when the exact seek fails.
            val exact = seeker.frameAt(uri, 0L)
            _state.update { current ->
                if (current.videoUri != uri || current.cover != null) return@update current
                val bitmap = exact ?: current.frames.firstOrNull()?.bitmap
                current.copy(cover = bitmap?.let { CoverFrame(index = 0, timeUs = 0L, bitmap = it) })
            }
        }
    }

    /** Opens the picker on the current cover's instant (or the start), and asks for that frame. */
    fun openCoverPicker() {
        val current = _state.value
        if (current.videoUri == null) return
        val start = current.cover?.takeIf { it.index != CoverFrame.UPLOADED }?.timeUs ?: 0L
        val preview = current.cover?.takeIf { it.index != CoverFrame.UPLOADED }?.bitmap
        _state.update { it.copy(picker = CoverPicker(timeUs = start, preview = preview, seeking = preview == null)) }
        if (preview == null) scrubs.tryEmit(start)
    }

    fun closeCoverPicker() = _state.update { it.copy(picker = null) }

    /**
     * The handle moved: the readout follows at once; the frame follows as
     * fast as the seeker answers, newest instant first.
     */
    fun onScrub(timeUs: Long) {
        val clamped = timeUs.coerceIn(0L, (_state.value.durationUs - Filmstrip.TAIL_MARGIN_US).coerceAtLeast(0L))
        _state.update { current ->
            current.picker?.let { current.copy(picker = it.copy(timeUs = clamped, seeking = true)) } ?: current
        }
        scrubs.tryEmit(clamped)
    }

    private suspend fun seekPreview(timeUs: Long) {
        // A drag emits many instants a second; one seek in flight is plenty,
        // and a short wait lets a burst settle on the last one.
        delay(SCRUB_SETTLE_MILLIS)
        val uri = _state.value.videoUri ?: return
        val bitmap = seeker.frameAt(uri, timeUs)
        _state.update { current ->
            val picker = current.picker ?: return@update current
            if (picker.timeUs != timeUs) {
                current
            } else {
                current.copy(picker = picker.copy(preview = bitmap, seeking = false))
            }
        }
    }

    /** The frame under the handle becomes the cover. */
    fun confirmCover() {
        val current = _state.value
        val picker = current.picker ?: return
        val bitmap = picker.preview ?: return
        val stripIndex = current.frames.indexOfLast { it.timeUs <= picker.timeUs }.coerceAtLeast(0)
        _state.update {
            it.copy(
                cover = CoverFrame(index = stripIndex, timeUs = picker.timeUs, bitmap = bitmap),
                coverSource = CoverSource.Frame,
                picker = null,
            )
        }
    }

    /** A gallery image, cropped to the kind's aspect, becomes the cover. */
    fun onCoverImagePicked(imageUri: String) {
        val aspect = _state.value.coverAspect
        viewModelScope.launch {
            val bitmap = images.load(imageUri, aspect) ?: run {
                fail("That image couldn't be read. Pick another.")
                return@launch
            }
            _state.update {
                it.copy(
                    cover = CoverFrame(index = CoverFrame.UPLOADED, timeUs = -1L, bitmap = bitmap),
                    coverSource = CoverSource.Upload,
                    picker = null,
                    phase = if (it.phase is Phase.Failure) Phase.Editing else it.phase,
                )
            }
        }
    }

    /**
     * The gate's way out for a reel over five minutes: the same video, the
     * same cover and every field, posted as a long video instead. The reel's
     * remix switch simply stops applying, and the channel is asked for.
     */
    fun switchToLong() {
        _state.update { it.copy(kind = VideoKind.LONG) }
        ensureChannel()
    }

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

    /** Null posts now; an instant inside [ScheduleWindow] schedules. The picker checks the window. */
    fun onScheduleChanged(publishAt: Instant?) = _state.update { it.copy(publishAt = publishAt) }

    // ── Hashtags ────────────────────────────────────────────────────────

    /**
     * Typing into the hashtag field: a space or comma after a tag turns it
     * into a chip at once ([Hashtags.shouldCommit]); otherwise the text
     * stays and, from two characters, asks the server for suggestions.
     */
    fun onHashtagInputChanged(value: String) {
        if (Hashtags.shouldCommit(value)) {
            commitHashtags(value)
            return
        }
        _state.update { it.copy(hashtagInput = value) }
        hashtagJob?.cancel()
        val query = value.trim().removePrefix("#")
        if (query.length < MIN_HASHTAG_QUERY) {
            _state.update { it.copy(hashtagSuggestions = emptyList()) }
            return
        }
        hashtagJob = viewModelScope.launch {
            delay(PEOPLE_DEBOUNCE_MILLIS)
            val found = lookups.suggestHashtags(query)
            _state.update { current ->
                val taken = current.hashtags.map { it.lowercase() }.toSet()
                current.copy(hashtagSuggestions = found.filter { it.lowercase() !in taken })
            }
        }
    }

    /** Whatever is in the field becomes chips (Done, or leaving the field). */
    fun commitHashtags(typed: String = _state.value.hashtagInput) {
        hashtagJob?.cancel()
        _state.update {
            it.copy(hashtags = Hashtags.add(it.hashtags, typed), hashtagInput = "", hashtagSuggestions = emptyList())
        }
    }

    /** A suggestion tapped becomes a chip and clears the field. */
    fun onHashtagSuggestionPicked(tag: String) = commitHashtags(tag)

    fun removeHashtag(tag: String) = _state.update { it.copy(hashtags = it.hashtags - tag) }

    // ── Mention people ──────────────────────────────────────────────────

    fun onPeopleQueryChanged(query: String) {
        _state.update { it.copy(peopleQuery = query) }
        peopleJob?.cancel()
        val trimmed = Hashtags.username(query)
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

        /** What the studio's export is: H.264 in MP4. */
        const val EXPORT_MIME_TYPE = "video/mp4"

        private const val MIN_PEOPLE_QUERY = 2
        private const val MIN_HASHTAG_QUERY = 2
        private const val PEOPLE_DEBOUNCE_MILLIS = 300L
        private const val SCRUB_SETTLE_MILLIS = 40L
        private const val MICROS_PER_MILLI = 1_000L
        private const val LANDSCAPE_ASPECT = 16f / 9f
        private const val PORTRAIT_ASPECT = 9f / 16f

        /** The Video tile opens the form as a long video; every other way in is a reel. */
        fun videoKindForSurface(routeKey: String?): VideoKind =
            if (routeKey == CreateSurface.Video.routeKey) VideoKind.LONG else VideoKind.REEL

        /**
         * The form, whole, as the record the worker publishes from. Any
         * text still in the hashtag field counts — a tag typed without a
         * trailing space is still a tag the user meant.
         */
        fun buildPending(current: ReelUiState, creationKey: String, coverPath: String?) = PendingReelPublish(
            creationKey = creationKey,
            videoUri = current.videoUri.orEmpty(),
            videoPath = current.exportedPath,
            videoMimeType = current.exportedPath?.let { EXPORT_MIME_TYPE },
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
            mentions = current.taggedUsers.map { it.username }.filter { it.isNotBlank() },
            locationName = current.locationName,
            hashtags = Hashtags.add(current.hashtags, current.hashtagInput),
            publishAt = current.publishAt?.let(ScheduleWindow::wire),
        )
    }
}
