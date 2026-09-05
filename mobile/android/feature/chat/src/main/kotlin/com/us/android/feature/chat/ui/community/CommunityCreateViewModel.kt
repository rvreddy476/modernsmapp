package com.us.android.feature.chat.ui.community

import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunityFailure
import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.CommunityRules
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

/** The create/edit form. [communityId] is blank for a create. */
data class CommunityFormUiState(
    val communityId: String = "",
    val loading: Boolean = false,
    val name: String = "",
    val handle: String = "",
    val description: String = "",
    val visibility: String = Community.VISIBILITY_PUBLIC,
    /** The chosen (not yet uploaded) picture, and the READY media id once it is. */
    val avatarUri: Uri? = null,
    val avatarMediaId: String? = null,
    /** The existing avatar, shown until a new one is picked. */
    val currentAvatarMediaId: String? = null,
    val uploadingAvatar: Boolean = false,
    /** Handles the server has refused with 409 — "live availability" learned from the answer. */
    val takenHandles: Set<String> = emptySet(),
    /** True once the handle was typed by hand; until then it follows the name. */
    val handleTouched: Boolean = false,
    val submitting: Boolean = false,
    val error: String? = null,
    val saved: Community? = null,
) {
    val isEdit: Boolean get() = communityId.isNotBlank()
    val nameProblem: String? get() = if (name.isEmpty()) null else CommunityRules.nameProblem(name)
    val handleProblem: String?
        get() = when {
            handle.isEmpty() -> null
            handle in takenHandles -> "That handle is taken."
            else -> CommunityRules.handleProblem(handle)
        }
    val descriptionProblem: String? get() = CommunityRules.descriptionProblem(description)
    val handleAvailable: Boolean get() = handle.isNotEmpty() && handleProblem == null
    val canSubmit: Boolean
        get() = !submitting && !uploadingAvatar &&
            CommunityRules.nameProblem(name) == null &&
            descriptionProblem == null &&
            (isEdit || (CommunityRules.handleProblem(handle) == null && handle !in takenHandles))
}

/**
 * Create a community, or edit one (owner/admin). The handle's availability
 * is what the server says: a `409 HANDLE_TAKEN` marks it taken in the form
 * at once and the field turns red until it changes. The avatar goes through
 * the chat attachment uploader — the same media authority every picture in
 * the app uses — and only a READY id is sent.
 */
@HiltViewModel
class CommunityCreateViewModel @Inject constructor(
    private val repository: CommunityRepository,
    private val uploader: ChatAttachmentUploader,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val _state = MutableStateFlow(
        CommunityFormUiState(communityId = savedStateHandle.get<String>("communityId").orEmpty()),
    )
    val state: StateFlow<CommunityFormUiState> = _state.asStateFlow()

    init {
        if (_state.value.isEdit) load()
    }

    private fun load() = viewModelScope.launch {
        _state.update { it.copy(loading = true) }
        when (val result = repository.community(_state.value.communityId)) {
            is AppResult.Success -> _state.update {
                val community = result.data
                it.copy(
                    loading = false,
                    name = community.name,
                    handle = community.handle,
                    description = community.description,
                    visibility = community.visibility.ifBlank { Community.VISIBILITY_PUBLIC },
                    currentAvatarMediaId = community.avatarMediaId,
                )
            }
            is AppResult.Failure -> _state.update { it.copy(loading = false, error = "Couldn't load the community.") }
        }
    }

    fun onNameChange(value: String) = _state.update {
        // A fresh community's handle follows its name until the handle is typed by hand.
        val handle = if (!it.isEdit && !it.handleTouched) CommunityRules.normaliseHandle(value) else it.handle
        it.copy(name = value.take(CommunityRules.NAME_MAX + 1), handle = handle, error = null)
    }

    fun onHandleChange(value: String) = _state.update {
        it.copy(handle = CommunityRules.normaliseHandle(value), handleTouched = true, error = null)
    }

    fun onDescriptionChange(value: String) = _state.update {
        it.copy(description = value.take(CommunityRules.DESCRIPTION_MAX + 1), error = null)
    }

    fun onVisibilityChange(value: String) = _state.update { it.copy(visibility = value) }

    /** A picked picture is uploaded at once so Save/Create never waits on it. */
    fun onAvatarPicked(uri: Uri?) {
        uri ?: return
        _state.update { it.copy(avatarUri = uri, avatarMediaId = null, uploadingAvatar = true, error = null) }
        viewModelScope.launch {
            when (val uploaded = uploader.uploadImage(uri)) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        avatarMediaId = uploaded.data,
                        uploadingAvatar = false
                    )
                }
                is AppResult.Failure -> _state.update {
                    it.copy(avatarUri = null, uploadingAvatar = false, error = "That picture couldn't be uploaded.")
                }
            }
        }
    }

    fun submit() {
        val form = _state.value
        if (!form.canSubmit) return
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            val result = if (form.isEdit) {
                repository.update(
                    id = form.communityId,
                    name = form.name.trim(),
                    description = form.description.trim(),
                    visibility = form.visibility,
                    avatarMediaId = form.avatarMediaId,
                )
            } else {
                repository.create(
                    name = form.name.trim(),
                    handle = form.handle,
                    description = form.description.trim(),
                    visibility = form.visibility,
                    avatarMediaId = form.avatarMediaId,
                )
            }
            when (result) {
                is AppResult.Success -> _state.update { it.copy(submitting = false, saved = result.data) }
                is AppResult.Failure -> _state.update {
                    when (result.error.toCommunityFailure()) {
                        CommunityFailure.HandleTaken ->
                            it.copy(submitting = false, takenHandles = it.takenHandles + form.handle)
                        CommunityFailure.Validation ->
                            it.copy(submitting = false, error = "Check the name, handle and description.")
                        CommunityFailure.NotAllowed ->
                            it.copy(submitting = false, error = "You can't change this community.")
                        CommunityFailure.Other ->
                            it.copy(submitting = false, error = "That didn't go through. Try again.")
                    }
                }
            }
        }
    }

    fun dismissError() = _state.update { it.copy(error = null) }
}
