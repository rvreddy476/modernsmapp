package com.us.android.feature.chat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One selectable connection. */
data class GroupCandidate(val userId: String, val displayName: String)

data class CreatedGroup(val conversationId: String, val title: String)

data class GroupCreateUiState(
    val title: String = "",
    val candidates: List<GroupCandidate> = emptyList(),
    val selectedIds: Set<String> = emptySet(),
    val loadingCandidates: Boolean = true,
    val creating: Boolean = false,
    val error: String? = null,
    val created: CreatedGroup? = null,
) {
    val canCreate: Boolean
        get() = title.isNotBlank() && selectedIds.isNotEmpty() && !creating
}

@HiltViewModel
class GroupCreateViewModel @Inject constructor(
    private val chatRepository: ChatRepository,
    private val profileRepository: ProfileRepository,
    private val store: ChatStore,
    authRepository: AuthRepository,
) : ViewModel() {

    private val viewerId: String =
        (authRepository.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    private val _state = MutableStateFlow(GroupCreateUiState())
    val state: StateFlow<GroupCreateUiState> = _state.asStateFlow()

    /**
     * The create's ONE idempotency key: retries of the same tap replay the
     * same group instead of minting a second one.
     */
    private var creationKey: String = ChatRepository.newIdempotencyKey()

    init {
        loadCandidates()
    }

    fun onTitleChange(value: String) = _state.update { it.copy(title = value, error = null) }

    fun toggle(userId: String) = _state.update {
        it.copy(
            selectedIds = if (userId in it.selectedIds) {
                it.selectedIds - userId
            } else {
                it.selectedIds + userId
            },
        )
    }

    fun create() {
        val current = _state.value
        if (!current.canCreate) return
        _state.update { it.copy(creating = true, error = null) }
        viewModelScope.launch {
            when (
                val result = chatRepository.createGroupGoverned(
                    title = current.title.trim(),
                    memberIds = current.selectedIds.toList(),
                    idempotencyKey = creationKey,
                )
            ) {
                is AppResult.Success -> {
                    store.syncInbox()
                    _state.update {
                        it.copy(
                            creating = false,
                            created = CreatedGroup(result.data.id, current.title.trim()),
                        )
                    }
                }
                is AppResult.Failure -> _state.update {
                    it.copy(creating = false, error = "The group couldn't be created. Try again.")
                }
            }
        }
    }

    /**
     * Candidates = the viewer's connections, names resolved via profiles.
     * Bounded (50) and parallel; a profile that fails to resolve still shows
     * as a selectable row with a neutral name rather than disappearing.
     */
    private fun loadCandidates() = viewModelScope.launch {
        val ids = when (val result = chatRepository.connections(viewerId)) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> {
                _state.update { it.copy(loadingCandidates = false) }
                return@launch
            }
        }
        val candidates = ids.map { id ->
            async {
                val name = (profileRepository.getProfile(id) as? AppResult.Success)
                    ?.data?.displayName?.takeIf { it.isNotBlank() }
                GroupCandidate(userId = id, displayName = name ?: "Connection")
            }
        }.awaitAll()
        _state.update { it.copy(loadingCandidates = false, candidates = candidates) }
    }
}
