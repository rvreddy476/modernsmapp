package com.us.android.feature.chat.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.common.result.AppResult
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
    val members: List<ConversationMember> = emptyList(),
    val viewerRole: String = "",
    val busyUserIds: Set<String> = emptySet(),
    val renaming: Boolean = false,
    val renameDraft: String = "",
    val notice: String? = null,
    /** Set once the viewer has left; the screen navigates away. */
    val left: Boolean = false,
) {
    val isOwner: Boolean get() = viewerRole == "owner"
    val isAdmin: Boolean get() = viewerRole == "admin" || isOwner
    val memberCount: Int get() = members.size
}

/**
 * Group info + administration (directive §3.4): roster with roles, add
 * member (policy-honest), remove, promote/demote (owner), ownership transfer
 * and leave. Every mutation refreshes from the server — the roster the user
 * sees is always the server's answer, never a local guess.
 */
@HiltViewModel
class GroupInfoViewModel @Inject constructor(
    private val repository: ChatRepository,
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
                        members = conv.members.sortedBy { m -> roleRank(m.role) },
                        viewerRole = conv.members.firstOrNull { m -> m.userId == viewerId }
                            ?.role.orEmpty(),
                    )
                }
            }
            is AppResult.Failure -> _state.update {
                it.copy(loading = false, notice = "Couldn't load group info.")
            }
        }
    }

    fun startRename() = _state.update { it.copy(renaming = true, renameDraft = it.title) }

    fun onRenameDraft(value: String) = _state.update { it.copy(renameDraft = value) }

    fun confirmRename() {
        val title = _state.value.renameDraft.trim()
        if (title.isBlank()) return
        _state.update { it.copy(renaming = false) }
        mutate("rename") { repository.updateGroupInfo(conversationId, title = title) }
    }

    fun cancelRename() = _state.update { it.copy(renaming = false) }

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
                is AppResult.Success -> refresh()
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
