package com.us.android.feature.chat.ui.community

import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.CommunityEvent
import com.us.android.core.chat.data.CommunityFailure
import com.us.android.core.chat.data.CommunityRules
import com.us.android.core.chat.data.CommunityUpdatesRepository
import com.us.android.core.chat.data.toCommunityFailure
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.ChatAttachmentUploader
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One picked picture on its way to being a media id. */
data class StagedPicture(val uri: Uri, val mediaId: String? = null, val failed: Boolean = false)

/** The admin composer. */
data class CommunityPostUiState(
    val communityId: String = "",
    val title: String = "",
    val body: String = "",
    val pictures: List<StagedPicture> = emptyList(),
    val eventOpen: Boolean = false,
    val eventTitle: String = "",
    val eventStartsAt: String = "",
    val eventEndsAt: String = "",
    val eventLocation: String = "",
    val posting: Boolean = false,
    val error: String? = null,
    val posted: Boolean = false,
) {
    val bodyProblem: String? get() = if (body.isEmpty()) null else CommunityRules.bodyProblem(body)
    val uploading: Boolean get() = pictures.any { it.mediaId == null && !it.failed }
    val eventProblem: String?
        get() = when {
            !eventOpen -> null
            eventTitle.isBlank() -> "Give the event a title."
            eventStartsAt.isBlank() -> "When does it start?"
            else -> null
        }
    val canPost: Boolean
        get() = !posting && !uploading && CommunityRules.bodyProblem(body) == null && eventProblem == null &&
            pictures.none { it.failed }
}

/**
 * Posts one update — body, up to a few pictures, an optional event — to a
 * community the viewer administers. Pictures upload as they are picked;
 * Post waits only for the ready ids. A 403 is the server saying the viewer
 * may not post here, and is said in those words.
 */
@HiltViewModel
class CommunityPostViewModel @Inject constructor(
    private val updates: CommunityUpdatesRepository,
    private val uploader: ChatAttachmentUploader,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val _state = MutableStateFlow(
        CommunityPostUiState(communityId = savedStateHandle.get<String>("communityId").orEmpty()),
    )
    val state: StateFlow<CommunityPostUiState> = _state.asStateFlow()

    fun onTitleChange(value: String) = _state.update { it.copy(title = value, error = null) }

    fun onBodyChange(value: String) = _state.update {
        it.copy(body = value.take(CommunityRules.UPDATE_BODY_MAX + 1), error = null)
    }

    fun toggleEvent() = _state.update { it.copy(eventOpen = !it.eventOpen, error = null) }

    fun onEventTitle(value: String) = _state.update { it.copy(eventTitle = value) }

    fun onEventStartsAt(value: String) = _state.update { it.copy(eventStartsAt = value) }

    fun onEventEndsAt(value: String) = _state.update { it.copy(eventEndsAt = value) }

    fun onEventLocation(value: String) = _state.update { it.copy(eventLocation = value) }

    fun stagePictures(uris: List<Uri>) {
        val fresh = uris.filter { uri -> _state.value.pictures.none { it.uri == uri } }
            .take(MAX_PICTURES - _state.value.pictures.size)
        if (fresh.isEmpty()) return
        _state.update { it.copy(pictures = it.pictures + fresh.map { uri -> StagedPicture(uri) }, error = null) }
        fresh.forEach { uri ->
            viewModelScope.launch {
                val result = uploader.uploadImage(uri)
                _state.update { current ->
                    current.copy(
                        pictures = current.pictures.map { picture ->
                            if (picture.uri != uri) {
                                picture
                            } else {
                                when (result) {
                                    is AppResult.Success -> picture.copy(mediaId = result.data)
                                    is AppResult.Failure -> picture.copy(failed = true)
                                }
                            }
                        },
                    )
                }
            }
        }
    }

    fun removePicture(uri: Uri) = _state.update { it.copy(pictures = it.pictures.filterNot { p -> p.uri == uri }) }

    fun post() {
        val form = _state.value
        if (!form.canPost) return
        _state.update { it.copy(posting = true, error = null) }
        viewModelScope.launch {
            val event = if (form.eventOpen) {
                CommunityEvent(
                    title = form.eventTitle.trim(),
                    startsAt = form.eventStartsAt.trim(),
                    endsAt = form.eventEndsAt.trim(),
                    location = form.eventLocation.trim(),
                )
            } else {
                null
            }
            val result = updates.post(
                id = form.communityId,
                body = form.body.trim(),
                mediaIds = form.pictures.mapNotNull { it.mediaId },
                title = form.title.trim().takeIf { it.isNotBlank() },
                event = event,
            )
            when (result) {
                is AppResult.Success -> _state.update { it.copy(posting = false, posted = true) }
                is AppResult.Failure -> _state.update {
                    it.copy(
                        posting = false,
                        error = when (result.error.toCommunityFailure()) {
                            CommunityFailure.NotAllowed -> "Only the community's admins can post here."
                            CommunityFailure.Validation -> "Check the text and the event details."
                            CommunityFailure.HandleTaken, CommunityFailure.Other -> "That didn't post. Try again."
                        },
                    )
                }
            }
        }
    }

    fun dismissError() = _state.update { it.copy(error = null) }

    private companion object {
        const val MAX_PICTURES = 4
    }
}
