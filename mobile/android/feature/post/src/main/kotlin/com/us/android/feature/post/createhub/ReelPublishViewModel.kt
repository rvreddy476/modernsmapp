package com.us.android.feature.post.createhub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
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
 * The Create hub's Reel surface: pick a video, describe it, choose a cover,
 * set who sees it and what viewers may do with it, post it as a `flick`.
 *
 * ## THE FORM (founder, 2026-09-04)
 *
 * TikTok-shaped. No title: the description is the only text, and the
 * server extracts hashtags from it, so the client sends text and nothing
 * derived from it. A cover strip of frames pulled from the video, one of
 * them chosen (the first by default). Tag people, a typed place name,
 * Audience, Category, and four switches sent EXPLICITLY on every post —
 * an omitted `allow_download` is "unspecified", not "true".
 *
 * ## POST HANDS OFF AND LEAVES
 *
 * Tapping Post no longer uploads here. A phone video took the dev server
 * seventeen minutes to transcode (2026-09-04) and the founder watched a
 * spinner the whole time, so the form now writes the chosen cover to disk,
 * hands a [PendingReelPublish] to the [ReelPublishLauncher] — WorkManager
 * from there, see [ReelPublishWorker] — and reports [Phase.Enqueued] so the
 * surface can close. The feed's banner shows the rest.
 */
@HiltViewModel
class ReelPublishViewModel @Inject constructor(
    private val launcher: ReelPublishLauncher,
    private val files: ReelPublishFiles,
    private val encoder: ReelCoverEncoder,
    private val frames: ReelFrameExtractor,
    private val lookups: ReelLookups,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
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
        val videoUri: String? = null,
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

        /** A media-only post is legal, so a chosen video is enough. */
        val canPost: Boolean
            get() = videoUri != null && (phase is Phase.Editing || phase is Phase.Failure)

        val canTagMore: Boolean
            get() = taggedUsers.size < MAX_TAGGED_PEOPLE
    }

    private val _state = MutableStateFlow(ReelUiState())
    val state: StateFlow<ReelUiState> = _state.asStateFlow()

    private var creationKey: String = UUID.randomUUID().toString()

    private var framesJob: Job? = null
    private var peopleJob: Job? = null

    init {
        viewModelScope.launch {
            lookups.categories()?.let { loaded -> _state.update { it.copy(categories = loaded) } }
        }
    }

    // ── Video and cover ─────────────────────────────────────────────────

    fun onVideoPicked(uri: String) {
        // A different video is a different post: new key, new frames.
        creationKey = UUID.randomUUID().toString()
        _state.update {
            it.copy(videoUri = uri, frames = emptyList(), framesLoading = true, coverIndex = 0, phase = Phase.Editing)
        }
        framesJob?.cancel()
        framesJob = viewModelScope.launch {
            val extracted = frames.extract(uri, COVER_FRAME_COUNT)
            _state.update { current ->
                if (current.videoUri == uri) current.copy(frames = extracted, framesLoading = false) else current
            }
        }
    }

    fun onCoverSelected(index: Int) = _state.update {
        if (index in it.frames.indices) it.copy(coverIndex = index) else it
    }

    // ── Fields ──────────────────────────────────────────────────────────

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
            fail("Your last reel is still posting. Try again when it's done.")
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

    private companion object {
        const val COVER_FRAME_COUNT = 6
        const val MIN_PEOPLE_QUERY = 2
        const val PEOPLE_DEBOUNCE_MILLIS = 300L

        /** The form, whole, as the record the worker publishes from. */
        fun buildPending(current: ReelUiState, creationKey: String, coverPath: String?) = PendingReelPublish(
            creationKey = creationKey,
            videoUri = current.videoUri.orEmpty(),
            coverPath = coverPath,
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
