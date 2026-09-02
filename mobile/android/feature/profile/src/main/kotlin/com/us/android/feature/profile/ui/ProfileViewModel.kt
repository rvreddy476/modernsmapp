package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ProfileRelationship
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class ProfileViewModel @Inject constructor(
    private val repository: ProfileRepository,
    sessionStateProvider: SessionStateProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    /**
     * Null means "the signed-in user". The route models it that way rather
     * than requiring the caller to know its own id, so the Me tab does not
     * have to wait for a session read before it can navigate.
     */
    private val userId: String? = savedStateHandle.profileUserId()

    /** The graph relationship endpoint wants the viewer's id explicitly. */
    private val viewerId: String =
        (sessionStateProvider.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    private val _state = MutableStateFlow<ProfileUiState>(ProfileUiState.Loading)
    val state: StateFlow<ProfileUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = ProfileUiState.Loading
        viewModelScope.launch {
            // Both requests go out together. Serialising them would put a
            // second round trip in front of first paint for data the header
            // can already render from the profile payload.
            val (profileResult, statsResult) = coroutineScope {
                val profile = async {
                    if (userId == null) repository.getOwnProfile() else repository.getProfile(userId)
                }
                // The stats id is only known for another user up front; for
                // the owner it needs the profile's id, so this call is issued
                // after the profile resolves in that case.
                val stats = async {
                    userId?.let { repository.getStats(it) }
                }
                profile.await() to stats.await()
            }

            when (profileResult) {
                is AppResult.Failure -> {
                    _state.value = ProfileUiState.Error(
                        message = ProfileErrorText.forLoad(profileResult.error),
                        retryable = ProfileErrorText.isRetryable(profileResult.error),
                    )
                }

                is AppResult.Success -> {
                    val profile = profileResult.data
                    // Stats failing must not fail the screen — the profile
                    // payload already carries follower/following/post counts,
                    // so the header degrades to those rather than to an error.
                    val stats = when {
                        statsResult is AppResult.Success -> statsResult.data
                        userId == null -> repository.getStats(profile.userId).let {
                            (it as? AppResult.Success)?.data
                        }
                        else -> null
                    }
                    // The REAL relationship, not a guess. This used to be
                    // hardcoded empty, so Follow reset to "Follow" on every
                    // visit no matter what the server knew. Best-effort: a
                    // graph blip degrades to the empty state, never an error.
                    val relationship = if (!profile.isOwnProfile && viewerId.isNotBlank()) {
                        val edge = repository.relationship(viewerId, profile.userId)
                        (edge as? AppResult.Success)?.data ?: ProfileRelationship()
                    } else {
                        ProfileRelationship()
                    }
                    _state.value = ProfileUiState.Content(
                        profile = profile,
                        stats = stats,
                        relationship = relationship,
                    )
                }
            }
        }
    }

    fun onFollowToggle() {
        val current = _state.value as? ProfileUiState.Content ?: return
        if (current.relationshipBusy || current.profile.isOwnProfile) return
        val target = current.profile.userId
        val wasFollowing = current.relationship.isFollowing

        // Optimistic: the button flips immediately and the count moves with
        // it, then rolls back on failure. Waiting for the round trip makes a
        // follow feel broken on a slow connection.
        _state.update {
            (it as? ProfileUiState.Content)?.copy(
                relationship = it.relationship.copy(isFollowing = !wasFollowing),
                relationshipBusy = true,
                actionError = null,
            ) ?: it
        }

        viewModelScope.launch {
            val result =
                if (wasFollowing) repository.unfollow(target) else repository.follow(target)
            _state.update { state ->
                val content = state as? ProfileUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> content.copy(relationshipBusy = false)
                    is AppResult.Failure -> content.copy(
                        relationship = content.relationship.copy(isFollowing = wasFollowing),
                        relationshipBusy = false,
                        actionError = ProfileErrorText.forRelationshipAction(result.error),
                    )
                }
            }
        }
    }

    fun onBlockToggle() {
        val current = _state.value as? ProfileUiState.Content ?: return
        if (current.relationshipBusy || current.profile.isOwnProfile) return
        val target = current.profile.userId
        val wasBlocked = current.relationship.isBlocked

        _state.update {
            (it as? ProfileUiState.Content)?.copy(
                relationshipBusy = true,
                actionError = null,
            ) ?: it
        }

        viewModelScope.launch {
            val result = if (wasBlocked) repository.unblock(target) else repository.block(target)
            _state.update { state ->
                val content = state as? ProfileUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> content.copy(
                        relationship = ProfileRelationship(
                            // Blocking someone ends the follow relationship
                            // server-side; reflecting that here keeps the two
                            // controls from contradicting each other.
                            isFollowing = if (wasBlocked) content.relationship.isFollowing else false,
                            isBlocked = !wasBlocked,
                        ),
                        relationshipBusy = false,
                    )

                    is AppResult.Failure -> content.copy(
                        relationshipBusy = false,
                        actionError = ProfileErrorText.forRelationshipAction(result.error),
                    )
                }
            }
        }
    }

    fun dismissActionError() = _state.update {
        (it as? ProfileUiState.Content)?.copy(actionError = null) ?: it
    }
}

/**
 * Reads the typed route argument out of [SavedStateHandle].
 *
 * Navigation Compose stores a `@Serializable` route's properties under their
 * own names, so the key is `userId`. Read directly rather than through
 * `toRoute<ProfileRoute>()` so the ViewModel can be constructed in a unit test
 * with a plain `SavedStateHandle(mapOf("userId" to ...))`, with no navigation
 * graph and no Robolectric.
 */
private fun SavedStateHandle.profileUserId(): String? = get<String>(USER_ID_KEY)

private const val USER_ID_KEY = "userId"
