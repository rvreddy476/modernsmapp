package com.us.android.feature.chat.ui.community

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.CommunityUpdate
import com.us.android.core.chat.data.CommunityUpdatesRepository
import com.us.android.core.chat.data.reactionAfterTap
import com.us.android.core.chat.data.withViewerReaction
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The community page: header facts and the updates feed. */
data class CommunityPageUiState(
    val communityId: String = "",
    val loading: Boolean = true,
    val community: Community? = null,
    val updates: List<CommunityUpdate> = emptyList(),
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    val busyMembership: Boolean = false,
    val busyMute: Boolean = false,
    /** Update ids whose reaction strip is open. */
    val reactingTo: String? = null,
    val message: UsMessage? = null,
    /** Set once the viewer left or the community was deleted; the screen closes. */
    val closed: Boolean = false,
    val loadFailed: Boolean = false,
)

/**
 * One community: header, updates newest first, reactions, views, join /
 * leave, mute, report. The server stays authoritative — every write is
 * followed by a re-read of the community so the header's counts and the
 * viewer's role are what the server says, and a reaction is applied
 * locally only in the shape the server will report it (one per viewer).
 */
@HiltViewModel
class CommunityPageViewModel @Inject constructor(
    private val repository: CommunityRepository,
    private val updates: CommunityUpdatesRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val _state = MutableStateFlow(
        CommunityPageUiState(communityId = savedStateHandle.get<String>("communityId").orEmpty()),
    )
    val state: StateFlow<CommunityPageUiState> = _state.asStateFlow()

    private val viewed = mutableSetOf<String>()

    init {
        refresh()
    }

    fun refresh() = viewModelScope.launch {
        val id = _state.value.communityId
        val community = repository.community(id)
        val page = updates.page(id)
        _state.update { current ->
            current.copy(
                loading = false,
                loadFailed = community is AppResult.Failure,
                community = (community as? AppResult.Success)?.data ?: current.community,
                updates = (page as? AppResult.Success)?.data?.items ?: current.updates,
                nextCursor = (page as? AppResult.Success)?.data?.nextCursor,
            )
        }
    }

    fun loadMore() {
        val cursor = _state.value.nextCursor ?: return
        if (_state.value.loadingMore) return
        _state.update { it.copy(loadingMore = true) }
        viewModelScope.launch {
            when (val page = updates.page(_state.value.communityId, cursor)) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        updates = (it.updates + page.data.items).distinctBy { row -> row.id },
                        nextCursor = page.data.nextCursor,
                        loadingMore = false,
                    )
                }
                is AppResult.Failure -> _state.update { it.copy(loadingMore = false) }
            }
        }
    }

    /** Posted once per update per screen life — the first time it is on screen. */
    fun onUpdateShown(updateId: String) {
        if (!viewed.add(updateId)) return
        viewModelScope.launch { updates.view(_state.value.communityId, updateId) }
    }

    fun toggleMembership() {
        val community = _state.value.community ?: return
        if (_state.value.busyMembership) return
        _state.update { it.copy(busyMembership = true) }
        viewModelScope.launch {
            val result = if (community.isMember) repository.leave(community.id) else repository.join(community.id)
            when (result) {
                is AppResult.Success -> {
                    reloadCommunity()
                    if (community.isMember) {
                        _state.update {
                            it.copy(
                                message = UsMessage("You left ${community.name}.", UsMessageType.Info)
                            )
                        }
                    }
                }
                is AppResult.Failure -> say(
                    if (community.isMember) "Couldn't leave right now." else "Couldn't join right now.",
                    UsMessageType.Error,
                )
            }
            _state.update { it.copy(busyMembership = false) }
        }
    }

    fun toggleMute() {
        val community = _state.value.community ?: return
        if (_state.value.busyMute) return
        _state.update { it.copy(busyMute = true) }
        viewModelScope.launch {
            val result = if (community.viewerMuted) repository.unmute(community.id) else repository.mute(community.id)
            when (result) {
                is AppResult.Success -> reloadCommunity()
                is AppResult.Failure -> say("Couldn't change notifications.", UsMessageType.Error)
            }
            _state.update { it.copy(busyMute = false) }
        }
    }

    fun openReactions(updateId: String) = _state.update {
        it.copy(reactingTo = if (it.reactingTo == updateId) null else updateId)
    }

    fun closeReactions() = _state.update { it.copy(reactingTo = null) }

    /**
     * One reaction per viewer: the same emoji again removes it, another
     * replaces it. Applied locally first, then confirmed; a refusal puts
     * the row back to what the server last said.
     */
    fun react(update: CommunityUpdate, emoji: String) {
        val next = update.reactionAfterTap(emoji)
        _state.update { current ->
            current.copy(
                reactingTo = null,
                updates = current.updates.map { if (it.id == update.id) it.withViewerReaction(next) else it },
            )
        }
        viewModelScope.launch {
            val id = _state.value.communityId
            val result = if (next == null) {
                updates.unreact(id, update.id, emoji)
            } else {
                updates.react(id, update.id, next)
            }
            if (result is AppResult.Failure) {
                _state.update { current ->
                    current.copy(updates = current.updates.map { if (it.id == update.id) update else it })
                }
                say("Couldn't react right now.", UsMessageType.Error)
            }
        }
    }

    fun report(reason: String, details: String) = viewModelScope.launch {
        when (repository.report(_state.value.communityId, reason, details)) {
            is AppResult.Success -> say("Thanks — we'll take a look.", UsMessageType.Success)
            is AppResult.Failure -> say("Couldn't send that report.", UsMessageType.Error)
        }
    }

    fun reportUpdate(updateId: String, reason: String, details: String) = viewModelScope.launch {
        when (updates.report(_state.value.communityId, updateId, reason, details)) {
            is AppResult.Success -> say("Thanks — we'll take a look.", UsMessageType.Success)
            is AppResult.Failure -> say("Couldn't send that report.", UsMessageType.Error)
        }
    }

    /** Owner only: deletes the community; the server refuses anyone else. */
    fun deleteCommunity() = viewModelScope.launch {
        when (repository.delete(_state.value.communityId)) {
            is AppResult.Success -> _state.update { it.copy(closed = true) }
            is AppResult.Failure -> say("Couldn't delete the community.", UsMessageType.Error)
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private suspend fun reloadCommunity() {
        when (val community = repository.community(_state.value.communityId)) {
            is AppResult.Success -> _state.update { it.copy(community = community.data) }
            is AppResult.Failure -> Unit
        }
    }

    private fun say(text: String, type: UsMessageType) = _state.update { it.copy(message = UsMessage(text, type)) }
}
