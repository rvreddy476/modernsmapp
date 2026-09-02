package com.us.android.feature.notifications.ui

import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationAddress

/**
 * Everything the inbox renders — Slice D.
 *
 * One state object, no parallel truth. The screen renders this and calls back;
 * it never holds its own copy of read-state or paging position.
 */
data class NotificationsUiState(
    val items: List<Notification> = emptyList(),
    /**
     * The server's whole-inbox unread count, adjusted optimistically.
     *
     * NOT derived from [items]. The badge must count notifications that have
     * not been fetched — a user with 200 unread and a 20-row first page would
     * otherwise see 20.
     */
    val unreadCount: Int = 0,
    val nextCursor: String? = null,
    val isLoading: Boolean = false,
    val isRefreshing: Boolean = false,
    val isLoadingMore: Boolean = false,
    /** Set when the first page failed. Later pages fail quietly. */
    val error: String? = null,
    /**
     * Conversations still waiting on this user's decision — the server's
     * answer, fetched alongside the page. A message-request row offers
     * Accept / Decline / Block only while its conversation is in here.
     */
    val pendingRequestIds: Set<String> = emptySet(),
    /** Actors the viewer already follows; their Follow rows show "Following". */
    val followingIds: Set<String> = emptySet(),
    /** Per-row action progress and outcome, keyed by notification id. */
    val rowActions: Map<String, RowActionState> = emptyMap(),
) {
    val isEmpty: Boolean get() = items.isEmpty() && !isLoading && error == null
    val hasMore: Boolean get() = nextCursor != null
    val hasUnread: Boolean get() = unreadCount > 0
}

/**
 * Where one row's inline action stands.
 *
 * The terminal states are kept rather than the row being removed: the user
 * just did something to that notification, and a row that vanishes under the
 * finger reads as a bug, not a success.
 */
enum class RowActionState { Busy, Failed, Followed, Accepted, Declined, Blocked }

/**
 * The inbox's rules, as pure functions — Slice D.
 *
 * ## WHY OPTIMISTIC READ-STATE NEEDS RULES RATHER THAN AN ASSIGNMENT
 *
 * Marking a notification read touches two things: the row, and a count the
 * server owns. Getting either wrong is visible and annoying:
 *
 *  - marking an ALREADY-READ row must not decrement the count. Tapping the same
 *    notification twice would otherwise drive the badge negative, and a badge
 *    showing "-1" is the kind of bug users screenshot;
 *  - a failed request must restore BOTH halves, not just the row;
 *  - the count must never go below zero even if the server and client disagree,
 *    because clamping is cheaper than being wrong in public.
 *
 * These are decisions, not plumbing, so they live here and are table-tested.
 * The ViewModel owns effects only.
 */
@Suppress("TooManyFunctions") // One rule per state transition, table-tested; splitting it would hide the table.
object NotificationsReducer {

    fun onLoadStarted(state: NotificationsUiState): NotificationsUiState =
        state.copy(isLoading = state.items.isEmpty(), isRefreshing = state.items.isNotEmpty(), error = null)

    fun onLoadMoreStarted(state: NotificationsUiState): NotificationsUiState =
        if (!state.hasMore || state.isLoadingMore) state else state.copy(isLoadingMore = true)

    /** First page, or a pull-to-refresh: replaces rather than appends. */
    fun onFirstPage(
        state: NotificationsUiState,
        items: List<Notification>,
        nextCursor: String?,
    ): NotificationsUiState = state.copy(
        items = items,
        nextCursor = nextCursor,
        isLoading = false,
        isRefreshing = false,
        error = null,
    )

    /**
     * A later page. Appends, de-duplicating by id.
     *
     * De-duplication is not defensive padding: the inbox is time-ordered and a
     * notification arriving between two page fetches shifts every subsequent
     * row, so the same id genuinely can appear twice. Compose would then throw
     * on duplicate keys in a keyed list.
     */
    fun onNextPage(
        state: NotificationsUiState,
        items: List<Notification>,
        nextCursor: String?,
    ): NotificationsUiState {
        val seen = state.items.mapTo(mutableSetOf()) { it.id }
        return state.copy(
            items = state.items + items.filter { seen.add(it.id) },
            nextCursor = nextCursor,
            isLoadingMore = false,
        )
    }

    fun onLoadFailed(state: NotificationsUiState, message: String): NotificationsUiState =
        state.copy(
            isLoading = false,
            isRefreshing = false,
            isLoadingMore = false,
            // A failed LATER page keeps the rows already on screen and says
            // nothing: the user has content, and an error banner over a working
            // list reads as though the whole inbox broke.
            error = if (state.items.isEmpty()) message else state.error,
        )

    fun onUnreadCount(state: NotificationsUiState, count: Int): NotificationsUiState =
        state.copy(unreadCount = count.coerceAtLeast(0))

    /**
     * Optimistically marks one row read.
     *
     * Returns the state unchanged when the row is missing or already read, so
     * the count cannot drift on a repeated tap.
     */
    fun onMarkedRead(state: NotificationsUiState, address: NotificationAddress): NotificationsUiState {
        val target = state.items.firstOrNull { it.address == address } ?: return state
        if (target.isRead) return state

        return state.copy(
            items = state.items.map { if (it.address == address) it.copy(isRead = true) else it },
            unreadCount = (state.unreadCount - 1).coerceAtLeast(0),
        )
    }

    /**
     * Puts a failed mark-read back.
     *
     * Both halves are restored together. Restoring only the row would leave the
     * badge permanently one short, which nothing would ever correct until the
     * next full refresh.
     */
    fun onMarkReadFailed(state: NotificationsUiState, address: NotificationAddress): NotificationsUiState {
        val target = state.items.firstOrNull { it.address == address } ?: return state
        if (!target.isRead) return state

        return state.copy(
            items = state.items.map { if (it.address == address) it.copy(isRead = false) else it },
            unreadCount = state.unreadCount + 1,
        )
    }

    /** Optimistically marks everything read. The count goes to zero, not to a guess. */
    fun onMarkedAllRead(state: NotificationsUiState): NotificationsUiState = state.copy(
        items = state.items.map { if (it.isRead) it else it.copy(isRead = true) },
        unreadCount = 0,
    )

    /**
     * Restores a failed mark-all-read.
     *
     * The previous state is passed back wholesale rather than recomputed: the
     * count included unread rows that were never loaded, and no amount of
     * inspecting [items] can recover that number.
     */
    fun onMarkAllReadFailed(previous: NotificationsUiState): NotificationsUiState = previous

    // ── Inline row actions ───────────────────────────────────────────────

    fun onPendingRequests(state: NotificationsUiState, ids: Set<String>): NotificationsUiState =
        state.copy(pendingRequestIds = ids)

    fun onFollowing(state: NotificationsUiState, ids: Set<String>): NotificationsUiState =
        state.copy(followingIds = state.followingIds + ids)

    fun onActionStarted(state: NotificationsUiState, notificationId: String): NotificationsUiState =
        state.copy(rowActions = state.rowActions + (notificationId to RowActionState.Busy))

    fun onActionFailed(state: NotificationsUiState, notificationId: String): NotificationsUiState =
        state.copy(rowActions = state.rowActions + (notificationId to RowActionState.Failed))

    /**
     * A confirmed outcome. The pending set and the following set are updated
     * HERE, from the same fact, so a row can never show "Accepted" and still
     * offer Accept.
     */
    fun onActionDone(
        state: NotificationsUiState,
        notification: Notification,
        outcome: RowActionState,
    ): NotificationsUiState = state.copy(
        rowActions = state.rowActions + (notification.id to outcome),
        pendingRequestIds = when (outcome) {
            RowActionState.Accepted,
            RowActionState.Declined,
            RowActionState.Blocked,
            -> state.pendingRequestIds - notification.entityId

            else -> state.pendingRequestIds
        },
        followingIds = if (outcome == RowActionState.Followed) {
            state.followingIds + notification.actorUserId
        } else {
            state.followingIds
        },
    )
}
