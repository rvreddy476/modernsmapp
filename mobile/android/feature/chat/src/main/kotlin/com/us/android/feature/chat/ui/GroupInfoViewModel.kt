package com.us.android.feature.chat.ui

import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.CommunityRules
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.GroupInviteRepository
import com.us.android.core.chat.data.InviteLink
import com.us.android.core.chat.data.InviteLinkState
import com.us.android.core.chat.data.inviteLinkState
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.ChatAttachmentUploader
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class GroupInfoUiState(
    val loading: Boolean = true,
    val title: String = "",
    val description: String = "",
    /** The SIGNED avatar URL from the latest read — five minutes of life, never cached. */
    val avatarUrl: String? = null,
    val members: List<ConversationMember> = emptyList(),
    val viewerRole: String = "",
    val busyUserIds: Set<String> = emptySet(),
    val renaming: Boolean = false,
    val renameDraft: String = "",
    val editingDescription: Boolean = false,
    val descriptionDraft: String = "",
    val uploadingPhoto: Boolean = false,
    /** The group's invite link as the server holds it; null when none is live. */
    val inviteLink: InviteLink? = null,
    val inviteLinkLoaded: Boolean = false,
    val inviteBusy: Boolean = false,
    val notice: String? = null,
    /** Set once the viewer has left; the screen navigates away. */
    val left: Boolean = false,
) {
    val isOwner: Boolean get() = viewerRole == "owner"
    val isAdmin: Boolean get() = viewerRole == "admin" || isOwner
    val memberCount: Int get() = members.size
    val descriptionProblem: String?
        get() = if (descriptionDraft.length > CommunityRules.GROUP_DESCRIPTION_MAX) {
            "At most ${CommunityRules.GROUP_DESCRIPTION_MAX} characters."
        } else {
            null
        }
    val inviteLinkState: InviteLinkState? get() = inviteLink?.let { inviteLinkState(it) }
}

/**
 * Group info + administration (directive §3.4, groups pass 2026-09-05):
 * roster with roles, rename, description, photo, invite link, add member
 * (policy-honest), remove, promote/demote (owner), ownership transfer and
 * leave. Every mutation refreshes from the server — the roster the user
 * sees is always the server's answer, never a local guess.
 */
@HiltViewModel
class GroupInfoViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val invites: GroupInviteRepository,
    private val uploader: ChatAttachmentUploader,
    private val store: ChatStore,
    authRepository: AuthRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    val conversationId: String = savedStateHandle.get<String>("conversationId").orEmpty()

    private val viewerId: String =
        (authRepository.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    private val _state = MutableStateFlow(GroupInfoUiState())
    val state: StateFlow<GroupInfoUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() = viewModelScope.launch {
        when (val result = repository.conversation(conversationId)) {
            is AppResult.Success -> {
                val conv = result.data
                _state.update {
                    it.copy(
                        loading = false,
                        title = conv.title.orEmpty(),
                        description = conv.description,
                        avatarUrl = conv.avatarUrl,
                        members = conv.members.sortedBy { m -> roleRank(m.role) },
                        viewerRole = conv.members.firstOrNull { m -> m.userId == viewerId }?.role.orEmpty(),
                    )
                }
                if (_state.value.isAdmin && !_state.value.inviteLinkLoaded) loadInviteLink()
            }
            is AppResult.Failure -> _state.update {
                it.copy(loading = false, notice = "Couldn't load group info.")
            }
        }
    }

    // ── Name ────────────────────────────────────────────────────────────

    fun startRename() = _state.update { it.copy(renaming = true, renameDraft = it.title) }

    fun onRenameDraft(value: String) = _state.update { it.copy(renameDraft = value) }

    fun confirmRename() {
        val title = _state.value.renameDraft.trim()
        if (title.isBlank()) return
        _state.update { it.copy(renaming = false) }
        mutate("rename") { repository.updateGroupInfo(conversationId, title = title) }
    }

    fun cancelRename() = _state.update { it.copy(renaming = false) }

    // ── Description ─────────────────────────────────────────────────────

    fun startDescription() = _state.update { it.copy(editingDescription = true, descriptionDraft = it.description) }

    fun onDescriptionDraft(value: String) = _state.update {
        it.copy(descriptionDraft = value.take(CommunityRules.GROUP_DESCRIPTION_MAX + 1))
    }

    fun confirmDescription() {
        val current = _state.value
        if (current.descriptionProblem != null) return
        _state.update { it.copy(editingDescription = false) }
        mutate(
            "description"
        ) { repository.updateGroupInfo(conversationId, description = current.descriptionDraft.trim()) }
    }

    fun cancelDescription() = _state.update { it.copy(editingDescription = false) }

    // ── Photo ───────────────────────────────────────────────────────────

    /** Pick → upload through the chat media authority → PUT the READY id. */
    fun changePhoto(uri: Uri?) {
        uri ?: return
        if (_state.value.uploadingPhoto) return
        _state.update { it.copy(uploadingPhoto = true) }
        viewModelScope.launch {
            when (val uploaded = uploader.uploadImage(uri)) {
                is AppResult.Success -> {
                    when (repository.updateGroupInfo(conversationId, avatarMediaId = uploaded.data)) {
                        is AppResult.Success -> refresh()
                        is AppResult.Failure -> _state.update { it.copy(notice = "The photo couldn't be set.") }
                    }
                }
                is AppResult.Failure -> _state.update { it.copy(notice = "That photo couldn't be uploaded.") }
            }
            _state.update { it.copy(uploadingPhoto = false) }
        }
    }

    // ── Invite link ─────────────────────────────────────────────────────

    private fun loadInviteLink() = viewModelScope.launch {
        when (val result = invites.current(conversationId)) {
            is AppResult.Success -> _state.update { it.copy(inviteLink = result.data, inviteLinkLoaded = true) }
            is AppResult.Failure -> _state.update {
                // No link is a normal answer; anything else is worth a line.
                if (result.error is AppError.NotFound) {
                    it.copy(inviteLink = null, inviteLinkLoaded = true)
                } else {
                    it.copy(inviteLinkLoaded = true, notice = "Couldn't check the invite link.")
                }
            }
        }
    }

    fun createInviteLink() = inviteMutation {
        when (val result = invites.create(conversationId)) {
            is AppResult.Success -> {
                _state.update { it.copy(inviteLink = result.data) }
                AppResult.Success(Unit)
            }
            is AppResult.Failure -> result
        }
    }

    fun revokeInviteLink() = inviteMutation {
        when (val result = invites.revoke(conversationId)) {
            is AppResult.Success -> {
                _state.update { it.copy(inviteLink = null, notice = "Invite link revoked.") }
                result
            }
            is AppResult.Failure -> result
        }
    }

    private fun inviteMutation(action: suspend () -> AppResult<Unit>) {
        if (_state.value.inviteBusy) return
        _state.update { it.copy(inviteBusy = true) }
        viewModelScope.launch {
            if (action() is AppResult.Failure) {
                _state.update { it.copy(notice = "That didn't go through. Try again.") }
            }
            _state.update { it.copy(inviteBusy = false) }
        }
    }

    // ── Roster ──────────────────────────────────────────────────────────

    fun addMember(userId: String) = mutate(userId) {
        when (val result = repository.addMember(conversationId, userId)) {
            is AppResult.Success -> {
                val text = when (result.data.outcome) {
                    "added" -> "Added."
                    "invited" -> "Invited — they'll join when they accept."
                    else -> "Their privacy settings don't allow this."
                }
                _state.update { it.copy(notice = text) }
                AppResult.Success(Unit)
            }
            is AppResult.Failure -> result
        }
    }

    fun removeMember(userId: String) = mutate(userId) {
        repository.removeMember(conversationId, userId)
    }

    fun promote(userId: String) = mutate(userId) {
        repository.setMemberRole(conversationId, userId, "admin")
    }

    fun demote(userId: String) = mutate(userId) {
        repository.setMemberRole(conversationId, userId, "member")
    }

    fun transferOwnership(userId: String) = mutate(userId) {
        repository.transferOwner(conversationId, userId)
    }

    fun leave() {
        _state.update { it.copy(busyUserIds = it.busyUserIds + viewerId) }
        viewModelScope.launch {
            when (repository.leave(conversationId)) {
                is AppResult.Success -> {
                    store.syncInbox()
                    _state.update { it.copy(left = true) }
                }
                is AppResult.Failure -> _state.update {
                    it.copy(
                        busyUserIds = it.busyUserIds - viewerId,
                        notice = "You can't leave yet — transfer ownership first.",
                    )
                }
            }
        }
    }

    fun dismissNotice() = _state.update { it.copy(notice = null) }

    private fun mutate(busyKey: String, action: suspend () -> AppResult<Unit>) {
        _state.update { it.copy(busyUserIds = it.busyUserIds + busyKey) }
        viewModelScope.launch {
            when (action()) {
                is AppResult.Success -> {
                    refresh()
                    // The inbox row carries the title and avatar; keep it in step.
                    store.syncInbox()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(notice = "That didn't go through. Try again.")
                }
            }
            _state.update { it.copy(busyUserIds = it.busyUserIds - busyKey) }
        }
    }

    private fun roleRank(role: String) = when (role) {
        "owner" -> 0
        "admin" -> 1
        else -> 2
    }
}
