package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.FollowListKind
import com.us.android.core.profile.data.FollowListPerson
import com.us.android.core.profile.data.FollowListRepository
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The two tabs of the list screen, in the order they are drawn. */
enum class FollowListTab(val wire: String, val label: String, val kind: FollowListKind) {
    FOLLOWERS("followers", "Followers", FollowListKind.FOLLOWERS),
    FOLLOWING("following", "Following", FollowListKind.FOLLOWING),
    ;

    companion object {
        fun fromWire(raw: String?): FollowListTab = entries.firstOrNull { it.wire == raw } ?: FOLLOWERS
    }
}

/** The relationship pill at the end of a row: the VIEWER's own edge toward that person. */
enum class FollowPill(val label: String, val filled: Boolean) {
    FOLLOW("Follow", filled = true),
    FOLLOW_BACK("Follow back", filled = true),
    FOLLOWING("Following", filled = false),
    REQUESTED("Requested", filled = false),
}

/**
 * Which pill a row wears, or none.
 *
 * None for the viewer's own row, and none while the edge is unknown — a pill
 * that appears and then changes when the real answer lands is worse than no
 * pill. "Follow back" only on the viewer's OWN Followers tab, where every row
 * is someone who already follows them.
 */
internal fun followPillFor(
    viewerId: String,
    personId: String,
    edge: FollowStatus?,
    tab: FollowListTab,
    isOwnList: Boolean,
): FollowPill? = when {
    viewerId.isBlank() || personId == viewerId -> null
    edge == null -> null
    edge == FollowStatus.FOLLOWING -> FollowPill.FOLLOWING
    edge == FollowStatus.REQUESTED -> FollowPill.REQUESTED
    isOwnList && tab == FollowListTab.FOLLOWERS -> FollowPill.FOLLOW_BACK
    else -> FollowPill.FOLLOW
}

/** One tab's list, paged. */
@Immutable
data class FollowListPage(
    val rows: List<FollowListPerson> = emptyList(),
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    /** The first page failed: the tab shows the error with Retry. */
    val error: String? = null,
    /** A later page failed: the rows stay, and the foot offers Retry. */
    val loadMoreFailed: Boolean = false,
    val nextCursor: String? = null,
    /** True once the first page answered, so an empty tab reads as empty, not as loading. */
    val loaded: Boolean = false,
) {
    val hasMore: Boolean get() = nextCursor != null
    val isEmpty: Boolean get() = loaded && rows.isEmpty() && error == null
}

/** A tap that asks first: unfollowing, or withdrawing a pending request. */
sealed interface FollowListConfirm {
    val person: FollowListPerson

    data class Unfollow(override val person: FollowListPerson) : FollowListConfirm
    data class CancelRequest(override val person: FollowListPerson) : FollowListConfirm
}

@Immutable
data class FollowListUiState(
    val handle: String = "",
    /** The list belongs to the signed-in viewer's own account. */
    val isOwnList: Boolean = false,
    val viewerId: String = "",
    val selected: FollowListTab = FollowListTab.FOLLOWERS,
    val followers: FollowListPage = FollowListPage(),
    val following: FollowListPage = FollowListPage(),
    /** People whose follow call is in flight; their pill shows a spinner and ignores taps. */
    val busyIds: Set<String> = emptySet(),
    val confirm: FollowListConfirm? = null,
    val actionError: String? = null,
) {
    fun page(tab: FollowListTab): FollowListPage = when (tab) {
        FollowListTab.FOLLOWERS -> followers
        FollowListTab.FOLLOWING -> following
    }

    fun pillFor(tab: FollowListTab, person: FollowListPerson): FollowPill? =
        followPillFor(viewerId, person.userId, person.viewerEdge, tab, isOwnList)

    internal fun withPage(tab: FollowListTab, transform: (FollowListPage) -> FollowListPage) = when (tab) {
        FollowListTab.FOLLOWERS -> copy(followers = transform(followers))
        FollowListTab.FOLLOWING -> copy(following = transform(following))
    }

    /** The viewer's edge toward [userId] changed: every row of that person, on both tabs. */
    internal fun withEdge(userId: String, edge: FollowStatus): FollowListUiState {
        fun FollowListPage.apply() = copy(
            rows = rows.map { if (it.userId == userId) it.copy(viewerEdge = edge) else it },
        )
        return copy(followers = followers.apply(), following = following.apply())
    }
}

/**
 * A profile's Followers / Following screen.
 *
 * The lists come from [FollowListRepository] (the graph's ids, hydrated with
 * names and the viewer's edges). Follow, unfollow and cancelling a request go
 * through the same [ProfileRepository] the profile header uses, optimistically:
 * the pill moves at once and is put back if the server says no. The profile
 * this screen was opened from re-reads its counts when it is shown again.
 */
@HiltViewModel
class FollowListViewModel @Inject constructor(
    private val lists: FollowListRepository,
    private val profiles: ProfileRepository,
    session: SessionStateProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val userId: String = savedStateHandle.get<String>(USER_ID_KEY).orEmpty()
    private val viewerId: String =
        (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    private val _state = MutableStateFlow(
        FollowListUiState(
            handle = savedStateHandle.get<String>(HANDLE_KEY).orEmpty(),
            isOwnList = viewerId.isNotBlank() && viewerId == userId,
            viewerId = viewerId,
            selected = FollowListTab.fromWire(savedStateHandle.get<String>(TAB_KEY)),
        ),
    )
    val state: StateFlow<FollowListUiState> = _state.asStateFlow()

    /** One page request per tab at a time. */
    private val pageJobs = mutableMapOf<FollowListTab, Job>()

    init {
        load(_state.value.selected)
    }

    /** Shows [tab]; its first page is fetched the first time it is shown. */
    fun select(tab: FollowListTab) {
        _state.update { it.copy(selected = tab) }
        val page = _state.value.page(tab)
        if (!page.loaded && !page.loading && page.error == null) load(tab)
    }

    /** Re-reads the tab on screen from its first page. */
    fun retry() = load(_state.value.selected)

    fun loadMore() {
        val tab = _state.value.selected
        val page = _state.value.page(tab)
        val cursor = page.nextCursor ?: return
        if (page.loadingMore || pageJobs[tab]?.isActive == true) return
        _state.update { s -> s.withPage(tab) { it.copy(loadingMore = true, loadMoreFailed = false) } }
        pageJobs[tab] = viewModelScope.launch {
            when (val result = lists.page(tab.kind, userId, viewerId, cursor)) {
                is AppResult.Success -> _state.update { s ->
                    s.withPage(tab) { current ->
                        val known = current.rows.mapTo(mutableSetOf()) { it.userId }
                        current.copy(
                            rows = current.rows + result.data.items.filterNot { it.userId in known },
                            nextCursor = result.data.nextCursor,
                            loadingMore = false,
                        )
                    }
                }
                // The loaded rows stay; the foot offers to try the same cursor again.
                is AppResult.Failure -> _state.update { s ->
                    s.withPage(tab) { it.copy(loadingMore = false, loadMoreFailed = true) }
                }
            }
        }
    }

    /** The row's pill was tapped: follow at once, or ask before undoing an edge. */
    fun onPillTap(personId: String) {
        val state = _state.value
        if (personId in state.busyIds) return
        val tab = state.selected
        val person = state.page(tab).rows.firstOrNull { it.userId == personId } ?: return
        when (state.pillFor(tab, person)) {
            FollowPill.FOLLOW, FollowPill.FOLLOW_BACK -> follow(person)
            FollowPill.FOLLOWING -> _state.update { it.copy(confirm = FollowListConfirm.Unfollow(person)) }
            FollowPill.REQUESTED -> _state.update { it.copy(confirm = FollowListConfirm.CancelRequest(person)) }
            null -> Unit
        }
    }

    fun onConfirm() {
        val confirm = _state.value.confirm ?: return
        _state.update { it.copy(confirm = null) }
        when (confirm) {
            is FollowListConfirm.Unfollow -> write(
                personId = confirm.person.userId,
                optimistic = FollowStatus.NONE,
                rollback = FollowStatus.FOLLOWING,
                call = { profiles.unfollow(confirm.person.userId) },
                settled = { FollowStatus.NONE },
            )
            is FollowListConfirm.CancelRequest -> write(
                personId = confirm.person.userId,
                optimistic = FollowStatus.NONE,
                rollback = FollowStatus.REQUESTED,
                call = { profiles.cancelFollowRequest(confirm.person.userId) },
                settled = { FollowStatus.NONE },
            )
        }
    }

    fun onDismissConfirm() = _state.update { it.copy(confirm = null) }

    fun dismissActionError() = _state.update { it.copy(actionError = null) }

    /**
     * Optimistically FOLLOWING; a private account answers "requested", and the
     * pill settles on that rather than claiming a follow that is still waiting.
     */
    private fun follow(person: FollowListPerson) = write(
        personId = person.userId,
        optimistic = FollowStatus.FOLLOWING,
        rollback = person.viewerEdge ?: FollowStatus.NONE,
        call = { profiles.follow(person.userId) },
        settled = { status -> FollowStatus.fromFollowResponse(status) },
    )

    private fun <T> write(
        personId: String,
        optimistic: FollowStatus,
        rollback: FollowStatus,
        call: suspend () -> AppResult<T>,
        settled: (T) -> FollowStatus,
    ) {
        _state.update { it.withEdge(personId, optimistic).copy(busyIds = it.busyIds + personId, actionError = null) }
        viewModelScope.launch {
            val result = call()
            _state.update { s ->
                val done = s.copy(busyIds = s.busyIds - personId)
                when (result) {
                    is AppResult.Success -> done.withEdge(personId, settled(result.data))
                    is AppResult.Failure -> done.withEdge(personId, rollback).copy(
                        actionError = ProfileErrorText.forRelationshipAction(result.error),
                    )
                }
            }
        }
    }

    private fun load(tab: FollowListTab) {
        pageJobs[tab]?.cancel()
        _state.update { s ->
            s.withPage(tab) { it.copy(loading = true, error = null, loadMoreFailed = false) }
        }
        pageJobs[tab] = viewModelScope.launch {
            when (val result = lists.page(tab.kind, userId, viewerId)) {
                is AppResult.Success -> _state.update { s ->
                    s.withPage(tab) {
                        FollowListPage(rows = result.data.items, nextCursor = result.data.nextCursor, loaded = true)
                    }
                }
                is AppResult.Failure -> _state.update { s ->
                    s.withPage(tab) {
                        it.copy(
                            loading = false,
                            error = if (it.rows.isEmpty()) MESSAGE_LOAD_FAILED else null,
                            loaded = it.rows.isNotEmpty(),
                        )
                    }
                }
            }
        }
    }

    internal companion object {
        const val USER_ID_KEY = "userId"
        const val HANDLE_KEY = "handle"
        const val TAB_KEY = "tab"
        const val MESSAGE_LOAD_FAILED = "Couldn't load this list."
    }
}
