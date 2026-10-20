package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.getOrNull
import com.us.android.core.model.FollowCounts
import com.us.android.core.model.FollowRequest
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileRelationship
import com.us.android.core.model.ProfileStats
import com.us.android.core.model.SessionState
import com.us.android.core.network.Paged
import com.us.android.core.profile.data.AuthorPostCountsRepository
import com.us.android.core.profile.data.GraphCountsRepository
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
    /** The live follow counts (graph-service, T-20); see [refreshCounts]. */
    private val graphCounts: GraphCountsRepository,
    /** The live post total (post-service, 2026-10-17): the profile payload's is never written. */
    private val postCounts: AuthorPostCountsRepository,
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
            val batch = firstReads()
            _state.value = when (val profileResult = batch.profile) {
                is AppResult.Failure -> ProfileUiState.Error(
                    message = ProfileErrorText.forLoad(profileResult.error),
                    retryable = ProfileErrorText.isRetryable(profileResult.error),
                )
                is AppResult.Success -> contentFor(profileResult.data, batch)
            }
        }
    }

    /**
     * All five first requests, out together. Serialising them would put
     * extra round trips in front of first paint for data the header can
     * already render from the profile payload.
     */
    private suspend fun firstReads(): LoadBatch = coroutineScope {
        val profile = async {
            if (userId == null) repository.getOwnProfile() else repository.getProfile(userId)
        }
        // The stats id is only known for another user up front; for the
        // owner it needs the profile's id, so this call is issued after the
        // profile resolves in that case.
        val stats = async { userId?.let { repository.getStats(it) } }
        // Only the own-profile screen shows the "Requests" pill — asking who
        // wants to follow SOMEONE ELSE is not a thing the viewer is
        // authorized to know.
        val requests = async { if (userId == null) repository.incomingFollowRequests() else null }
        // The graph's live follow counts (T-20). Like stats, the owner's are
        // read once the profile names the id.
        val counts = async { userId?.let { graphCounts.counts(it) } }
        // post-service's post total, read the same way.
        val posts = async { userId?.let { postCounts.total(it) } }
        LoadBatch(profile.await(), stats.await(), requests.await(), counts.await(), posts.await())
    }

    private suspend fun contentFor(profile: Profile, batch: LoadBatch): ProfileUiState.Content {
        // Stats failing must not fail the screen — the profile payload
        // already carries follower/following/post counts, so the header
        // degrades to those rather than to an error.
        val stats = (batch.stats ?: repository.getStats(profile.userId)).getOrNull()
        // Best-effort, like stats: a failed read leaves the profile-side
        // follow counts on the header.
        val liveCounts = (batch.counts ?: graphCounts.counts(profile.userId)).getOrNull()
        // The same for the post total: a failed read keeps the profile side's number.
        val postTotal = (batch.posts ?: postCounts.total(profile.userId)).getOrNull()
        // The REAL relationship, not a guess. This used to be hardcoded
        // empty, so Follow reset to "Follow" on every visit no matter what
        // the server knew. Best-effort: a graph blip degrades to what the
        // PROFILE payload already said about privacy and follow status,
        // never an error.
        val relationship = if (!profile.isOwnProfile && viewerId.isNotBlank()) {
            repository.relationship(viewerId, profile.userId).getOrNull() ?: profile.fallbackRelationship()
        } else {
            ProfileRelationship(isPrivate = profile.isPrivate)
        }
        return ProfileUiState.Content(
            profile = profile,
            stats = stats,
            relationship = relationship,
            // The count from whichever page loaded; a failed fetch leaves the
            // pill absent rather than claiming zero.
            incomingFollowRequestCount = batch.requests?.getOrNull()?.items?.size,
            graphCounts = liveCounts,
            postTotal = postTotal,
        )
    }

    /**
     * Follow, cancel a pending request, or unfollow — whichever the current
     * [FollowStatus] means the tap is for.
     *
     * NONE and FOLLOWING act immediately, same as before private accounts
     * existed. REQUESTED does not: cancelling a request the other person may
     * already be about to approve is destructive enough to confirm first, so
     * this only arms that confirmation — [onConfirmCancelRequest] is what
     * actually cancels it.
     */
    fun onFollowToggle() {
        val current = _state.value as? ProfileUiState.Content ?: return
        if (current.relationshipBusy || current.profile.isOwnProfile || current.unavailable) return

        when (current.relationship.followStatus) {
            FollowStatus.NONE -> startFollow(current)
            FollowStatus.FOLLOWING -> unfollow(current)
            FollowStatus.REQUESTED -> _state.update {
                (it as? ProfileUiState.Content)?.copy(showCancelRequestConfirm = true) ?: it
            }
        }
    }

    fun onDismissCancelRequestConfirm() = _state.update {
        (it as? ProfileUiState.Content)?.copy(showCancelRequestConfirm = false) ?: it
    }

    fun onConfirmCancelRequest() {
        val current = _state.value as? ProfileUiState.Content ?: return
        if (current.relationshipBusy) return
        val target = current.profile.userId

        _state.update {
            (it as? ProfileUiState.Content)?.copy(
                relationship = it.relationship.copy(followStatus = FollowStatus.NONE),
                relationshipBusy = true,
                showCancelRequestConfirm = false,
                actionError = null,
            ) ?: it
        }

        viewModelScope.launch {
            val result = repository.cancelFollowRequest(target)
            _state.update { state ->
                val content = state as? ProfileUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> content.copy(relationshipBusy = false)
                    is AppResult.Failure -> content.copy(
                        relationship = content.relationship.copy(followStatus = FollowStatus.REQUESTED),
                        relationshipBusy = false,
                        actionError = ProfileErrorText.forRelationshipAction(result.error),
                    )
                }
            }
        }
    }

    /**
     * Optimistic: the button flips immediately — to REQUESTED for a private
     * target, FOLLOWING for a public one — then settles on whatever the
     * server actually answered, or rolls back on failure. Waiting for the
     * round trip makes a follow feel broken on a slow connection.
     */
    private fun startFollow(current: ProfileUiState.Content) {
        val target = current.profile.userId
        val optimistic = if (current.profile.isPrivate) FollowStatus.REQUESTED else FollowStatus.FOLLOWING

        _state.update {
            (it as? ProfileUiState.Content)?.copy(
                relationship = it.relationship.copy(
                    followStatus = optimistic,
                    isFollowing = optimistic == FollowStatus.FOLLOWING,
                ),
                relationshipBusy = true,
                actionError = null,
            ) ?: it
        }

        viewModelScope.launch {
            val result = repository.follow(target)
            _state.update { state ->
                val content = state as? ProfileUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> {
                        val confirmed = FollowStatus.fromFollowResponse(result.data)
                        content.copy(
                            relationship = content.relationship.copy(
                                followStatus = confirmed,
                                isFollowing = confirmed == FollowStatus.FOLLOWING,
                            ),
                            relationshipBusy = false,
                        )
                    }

                    is AppResult.Failure -> content.copy(
                        relationship = content.relationship.copy(
                            followStatus = FollowStatus.NONE,
                            isFollowing = false,
                        ),
                        relationshipBusy = false,
                        actionError = ProfileErrorText.forRelationshipAction(result.error),
                    )
                }
            }
            if (result is AppResult.Success) refreshCounts(target)
        }
    }

    private fun unfollow(current: ProfileUiState.Content) {
        val target = current.profile.userId

        _state.update {
            (it as? ProfileUiState.Content)?.copy(
                relationship = it.relationship.copy(followStatus = FollowStatus.NONE, isFollowing = false),
                relationshipBusy = true,
                actionError = null,
            ) ?: it
        }

        viewModelScope.launch {
            val result = repository.unfollow(target)
            _state.update { state ->
                val content = state as? ProfileUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> content.copy(relationshipBusy = false)
                    is AppResult.Failure -> content.copy(
                        relationship = content.relationship.copy(
                            followStatus = FollowStatus.FOLLOWING,
                            isFollowing = true,
                        ),
                        relationshipBusy = false,
                        actionError = ProfileErrorText.forRelationshipAction(result.error),
                    )
                }
            }
            if (result is AppResult.Success) refreshCounts(target)
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
                        relationship = content.relationship.copy(
                            // Blocking someone ends the follow relationship
                            // server-side; reflecting that here keeps the two
                            // controls from contradicting each other. isPrivate
                            // is left untouched — blocking someone does not
                            // change whether THEIR account is private.
                            isFollowing = if (wasBlocked) content.relationship.isFollowing else false,
                            followStatus = if (wasBlocked) content.relationship.followStatus else FollowStatus.NONE,
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
            if (result is AppResult.Success) refreshCounts(target)
        }
    }

    /**
     * The screen is shown again — back from the follower lists, where the
     * viewer may have followed or unfollowed people — so the header's live
     * counts are read again. Nothing happens before the profile has loaded.
     */
    fun refreshFollowCounts() {
        val content = _state.value as? ProfileUiState.Content ?: return
        viewModelScope.launch { refreshCounts(content.profile.userId) }
    }

    fun dismissActionError() = _state.update {
        (it as? ProfileUiState.Content)?.copy(actionError = null) ?: it
    }

    /**
     * Re-reads the graph's counts for the profile on screen after a follow,
     * unfollow, block or unblock went through (T-20), so the header moves
     * with the action. graph-service drops its 60 s count cache for both
     * accounts on each of those writes, so this read is already fresh. A
     * failed read keeps the numbers the header had; a profile that changed
     * meanwhile is left alone.
     */
    private suspend fun refreshCounts(userId: String) {
        val fresh = (graphCounts.counts(userId) as? AppResult.Success)?.data ?: return
        _state.update { state ->
            val content = state as? ProfileUiState.Content
            if (content == null || content.profile.userId != userId) state else content.copy(graphCounts = fresh)
        }
    }
}

/**
 * The relationship the screen renders when the graph call itself is
 * unavailable — best-effort, seeded from what the PROFILE payload already
 * disclosed rather than a blank slate that would reset "Requested" back to
 * "Follow" on a blip.
 */
private fun Profile.fallbackRelationship() = ProfileRelationship(
    isFollowing = followStatus == FollowStatus.FOLLOWING,
    isPrivate = isPrivate,
    followStatus = followStatus,
)

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

/** The four first reads of [ProfileViewModel.load], awaited together. */
private data class LoadBatch(
    val profile: AppResult<Profile>,
    val stats: AppResult<ProfileStats>?,
    val requests: AppResult<Paged<FollowRequest>>?,
    val counts: AppResult<FollowCounts>?,
    val posts: AppResult<Int>?,
)
