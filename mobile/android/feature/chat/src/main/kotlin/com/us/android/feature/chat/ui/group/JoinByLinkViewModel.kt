package com.us.android.feature.chat.ui.group

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.GroupInviteRepository
import com.us.android.core.chat.data.InviteFailure
import com.us.android.core.chat.data.InviteLinkState
import com.us.android.core.chat.data.InvitePreview
import com.us.android.core.chat.data.inviteCodeFrom
import com.us.android.core.chat.data.invitePreviewState
import com.us.android.core.chat.data.toInviteFailure
import com.us.android.core.common.result.AppResult
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The conversation a join opened, handed to the host once. */
data class JoinedGroup(val conversationId: String, val title: String)

data class JoinByLinkUiState(
    /** What the user pasted, or the code the deep link arrived with. */
    val input: String = "",
    val code: String? = null,
    val loading: Boolean = false,
    val preview: InvitePreview? = null,
    val previewState: InviteLinkState? = null,
    val problem: String? = null,
    val joining: Boolean = false,
    val joined: JoinedGroup? = null,
) {
    val canJoin: Boolean get() = preview != null && previewState == InviteLinkState.Live && !joining
    val canOpen: Boolean get() = previewState == InviteLinkState.Member
}

/**
 * Join a group by its link: `atpost.app/chat/join/{code}` from outside, or
 * a pasted link / code from the ≡ menu. The code is previewed first —
 * title, avatar, member count — and the sheet says plainly when a link is
 * dead, exhausted, or already the viewer's own group.
 */
@HiltViewModel
class JoinByLinkViewModel @Inject constructor(
    private val invites: GroupInviteRepository,
    private val store: ChatStore,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val _state = MutableStateFlow(JoinByLinkUiState())
    val state: StateFlow<JoinByLinkUiState> = _state.asStateFlow()

    init {
        val code = savedStateHandle.get<String>("code").orEmpty()
        if (code.isNotBlank()) {
            _state.update { it.copy(input = code) }
            lookUp()
        }
    }

    fun onInputChange(value: String) = _state.update { it.copy(input = value, problem = null) }

    /** Reads the code out of whatever was pasted and asks the server what it is. */
    fun lookUp() {
        val code = inviteCodeFrom(_state.value.input)
        if (code == null) {
            _state.update { it.copy(problem = "That doesn't look like an invite link.") }
            return
        }
        _state.update { it.copy(code = code, loading = true, problem = null, preview = null, previewState = null) }
        viewModelScope.launch {
            when (val result = invites.preview(code)) {
                is AppResult.Success -> _state.update {
                    it.copy(loading = false, preview = result.data, previewState = invitePreviewState(result.data))
                }
                is AppResult.Failure -> _state.update {
                    it.copy(loading = false, problem = failureText(result.error.toInviteFailure()))
                }
            }
        }
    }

    fun join() {
        val current = _state.value
        val code = current.code ?: return
        if (!current.canJoin) return
        _state.update { it.copy(joining = true, problem = null) }
        viewModelScope.launch {
            when (val result = invites.join(code)) {
                is AppResult.Success -> {
                    store.syncInbox()
                    _state.update {
                        it.copy(
                            joining = false,
                            joined = JoinedGroup(
                                result.data.id,
                                result.data.title.orEmpty().ifBlank { current.preview?.title.orEmpty() }
                            ),
                        )
                    }
                }
                is AppResult.Failure -> _state.update {
                    it.copy(joining = false, problem = failureText(result.error.toInviteFailure()))
                }
            }
        }
    }

    /** Already a member: open the group the preview points at. */
    fun openExisting() {
        val preview = _state.value.preview ?: return
        _state.update { it.copy(joined = JoinedGroup(preview.conversationId, preview.title)) }
    }

    fun consumeJoined() = _state.update { it.copy(joined = null) }

    private fun failureText(failure: InviteFailure): String = when (failure) {
        InviteFailure.NotFound -> "This invite link doesn't exist."
        InviteFailure.NotLive -> "This invite link has expired or was revoked."
        InviteFailure.NotAllowed -> "You can't join this group with a link."
        InviteFailure.GroupFull -> "This group is full."
        InviteFailure.Other -> "Couldn't reach the group right now. Try again."
    }
}
