package com.us.android.feature.post.createhub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CONTENT_TYPE_FLICK
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_VIDEO
import com.us.android.feature.post.data.dto.REMIX_ALLOW
import com.us.android.feature.post.data.dto.REMIX_DISALLOW
import com.us.android.feature.post.data.dto.SupportedAudience
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
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
 * ## THE PIPELINE
 *
 * The composer's media discipline, twice: the video (`file_type: video`,
 * transcode-sized readiness window) and then the chosen cover as a JPEG
 * through the image path, each polled for EXACT ready+passed, then ONE
 * create through the one create call site. A cover that fails to upload
 * FAILS THE POST with a retryable message — posting without the cover the
 * user chose would be publishing something other than what they approved.
 * Both ready ids survive a create retry, so a retry never re-uploads.
 *
 * ## WHAT THIS DOES NOT DO YET
 *
 * No trimming or effects, and the upload runs in this ViewModel's scope, so
 * backgrounding mid-upload can lose it; a WorkManager continuation like the
 * Studio's is tracked in the handover.
 */
@HiltViewModel
class ReelPublishViewModel @Inject constructor(
    private val repository: ComposerRepository,
    private val uploads: ReelMediaUploads,
    private val videoSources: MediaSourceResolver,
    private val frames: ReelFrameExtractor,
    private val lookups: ReelLookups,
) : ViewModel() {

    sealed interface Phase {
        data object Editing : Phase
        data class Uploading(val fraction: Float) : Phase
        data object Processing : Phase
        data object Posting : Phase
        data class Published(val postId: String) : Phase
        data class Failure(val message: String, val retryable: Boolean) : Phase
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
            get() = phase is Phase.Uploading || phase is Phase.Processing || phase is Phase.Posting

        /** A media-only post is legal, so a chosen video is enough. */
        val canPost: Boolean
            get() = videoUri != null && (phase is Phase.Editing || phase is Phase.Failure)

        val canTagMore: Boolean
            get() = taggedUsers.size < MAX_TAGGED_PEOPLE
    }

    private val _state = MutableStateFlow(ReelUiState())
    val state: StateFlow<ReelUiState> = _state.asStateFlow()

    private var creationKey: String = UUID.randomUUID().toString()

    /** A confirmed+ready video id survives a create retry — no re-upload. */
    private var readyVideoId: String? = null

    /** Likewise the cover, keyed to the frame it was made from. */
    private var readyCover: Pair<Int, String>? = null

    private var framesJob: Job? = null
    private var peopleJob: Job? = null

    init {
        viewModelScope.launch {
            lookups.categories()?.let { loaded -> _state.update { it.copy(categories = loaded) } }
        }
    }

    // ── Video and cover ─────────────────────────────────────────────────

    fun onVideoPicked(uri: String) {
        // A different video is a different post: new key, new uploads, new frames.
        creationKey = UUID.randomUUID().toString()
        readyVideoId = null
        readyCover = null
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
        viewModelScope.launch {
            val videoId = readyVideoId ?: uploadVideo(current.videoUri!!) ?: return@launch
            readyVideoId = videoId
            val coverId = uploadCoverIfChosen(current) ?: return@launch
            createFlick(videoId, coverId.takeIf { it.isNotEmpty() }, current)
        }
    }

    private suspend fun uploadVideo(uri: String): String? {
        val picked = videoSources.resolve(uri)
        if (picked == null) {
            fail("That video can't be read.", retryable = false)
            return null
        }
        _state.update { it.copy(phase = Phase.Uploading(0f)) }
        val outcome = uploads.uploadVideo(
            video = picked,
            onProgress = { fraction -> _state.update { it.copy(phase = Phase.Uploading(fraction)) } },
            onProcessing = { _state.update { it.copy(phase = Phase.Processing) } },
        )
        return when (outcome) {
            is ReelMediaUploads.Outcome.Ready -> outcome.mediaId
            is ReelMediaUploads.Outcome.Failed -> {
                fail(outcome.message, outcome.retryable)
                null
            }
        }
    }

    /**
     * Empty string when there is no cover to send (no frame could be
     * extracted); null when the cover upload failed and the post must stop.
     */
    private suspend fun uploadCoverIfChosen(current: ReelUiState): String? {
        val frame = current.cover ?: return ""
        readyCover?.takeIf { it.first == frame.index }?.let { return it.second }
        _state.update { it.copy(phase = Phase.Posting) }
        return when (val outcome = uploads.uploadCover(frame)) {
            is ReelMediaUploads.Outcome.Ready -> {
                readyCover = frame.index to outcome.mediaId
                outcome.mediaId
            }
            is ReelMediaUploads.Outcome.Failed -> {
                fail(outcome.message, outcome.retryable)
                null
            }
        }
    }

    private suspend fun createFlick(videoId: String, coverId: String?, current: ReelUiState) {
        _state.update { it.copy(phase = Phase.Posting) }
        when (val result = repository.createPost(creationKey, buildRequest(current, videoId, coverId))) {
            is AppResult.Success -> {
                creationKey = UUID.randomUUID().toString()
                readyVideoId = null
                readyCover = null
                _state.update { it.copy(phase = Phase.Published(result.data)) }
            }
            is AppResult.Failure ->
                fail(repository.message(result.error), retryable = !repository.isTerminal(result.error))
        }
    }

    private fun fail(message: String, retryable: Boolean) =
        _state.update { it.copy(phase = Phase.Failure(message, retryable)) }

    private companion object {
        const val DEFAULT_LANGUAGE = "en"
        const val COVER_FRAME_COUNT = 6
        const val MIN_PEOPLE_QUERY = 2
        const val PEOPLE_DEBOUNCE_MILLIS = 300L

        /**
         * The ONE place the form becomes bytes. The switches go on the wire
         * whatever their value; the optional fields are omitted when unset so
         * an empty category is "none", not `""`.
         */
        fun buildRequest(current: ReelUiState, videoId: String, coverId: String?) = CreatePostRequest(
            text = current.caption.trim(),
            visibility = current.visibility,
            contentType = CONTENT_TYPE_FLICK,
            postType = POST_TYPE_VIDEO,
            mediaIds = listOf(videoId),
            language = DEFAULT_LANGUAGE,
            distribution = DistributionRequest(),
            title = "",
            noComments = !current.allowComments,
            hideShare = current.hideShare,
            allowDownload = current.allowDownload,
            remixSetting = if (current.allowRemix) REMIX_ALLOW else REMIX_DISALLOW,
            category = current.category.trim().ifBlank { null },
            coverMediaId = coverId,
            taggedUserIds = current.taggedUsers.map { it.id }.takeIf { it.isNotEmpty() },
            locationName = current.locationName.trim().ifBlank { null },
        )
    }
}
