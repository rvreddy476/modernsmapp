package com.us.android.feature.notifications.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationAddress
import com.us.android.core.model.NotificationKind
import com.us.android.core.notifications.data.NotificationsRepository
import com.us.android.core.notifications.data.UnreadBadge
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Drives the notification inbox — Slice D.
 *
 * Meaning lives in [NotificationsReducer], which is table-tested. This class
 * owns effects: the network, the single in-flight page job, and the ordering
 * between an optimistic edit and the request that confirms it.
 *
 * ## READ-STATE IS OPTIMISTIC, AND RECONCILED
 *
 * A tap marks the row read immediately, then calls the server. On failure the
 * edit is reverted — BOTH the row and the count, together. The server remains
 * the authority: every refresh re-reads the count rather than trusting the
 * local arithmetic, so a client that drifted is corrected on the next load
 * instead of staying wrong until reinstall.
 *
 * ## ROW ACTIONS ARE NOT OPTIMISTIC
 *
 * Accept / Decline / Block / Follow wait for the server. Read-state can be
 * guessed and quietly corrected; "you are now connected to a stranger" cannot.
 * The row shows a busy state until the answer arrives, then the outcome.
 */
@HiltViewModel
class NotificationsViewModel @Inject constructor(
    private val repository: NotificationsRepository,
    /**
     * The shared badge — Slice D.
     *
     * Written here, read by the feed's top bar. Without it, clearing the
     * inbox would leave the feed still showing the old count until it happened
     * to refetch, which reads as the app being broken.
     */
    private val badge: UnreadBadge,
    private val actions: NotificationActions,
) : ViewModel() {

    private val _state = MutableStateFlow(NotificationsUiState())
    val state: StateFlow<NotificationsUiState> = _state.asStateFlow()

    /** The one in-flight page request. A second refresh cannot start a second. */
    private var pageJob: Job? = null

    init {
        refresh()
    }

    /**
     * Loads the first page and the unread count.
     *
     * Both, every time. The count is not derived from the page, so a refresh
     * that only reloaded rows would leave a stale badge above a fresh list.
     */
    fun refresh() {
        if (pageJob?.isActive == true) return
        _state.value = NotificationsReducer.onLoadStarted(_state.value)

        pageJob = viewModelScope.launch {
            when (val result = repository.page()) {
                is AppResult.Success -> {
                    _state.value = NotificationsReducer.onFirstPage(
                        _state.value,
                        result.data.items,
                        result.data.nextCursor,
                    )
                    hydrateRowActions(result.data.items, includeRequests = true)
                }

                is AppResult.Failure -> {
                    _state.value = NotificationsReducer.onLoadFailed(_state.value, MESSAGE_LOAD_FAILED)
                }
            }
            loadUnreadCount()
            publishBadge()
        }
    }

    /**
     * Fetches the next page.
     *
     * Guarded by the reducer rather than here: "there is no next page" and "one
     * is already loading" are both facts about state, and deciding them in two
     * places is how a list ends up firing duplicate requests at the bottom.
     */
    fun loadMore() {
        val current = _state.value
        val cursor = current.nextCursor ?: return
        if (current.isLoadingMore || pageJob?.isActive == true) return

        _state.value = NotificationsReducer.onLoadMoreStarted(current)
        pageJob = viewModelScope.launch {
            when (val result = repository.page(cursor = cursor)) {
                is AppResult.Success -> {
                    _state.value = NotificationsReducer.onNextPage(
                        _state.value,
                        result.data.items,
                        result.data.nextCursor,
                    )
                    // The pending set is whole-inbox and already loaded; only
                    // the follow edges are per-actor.
                    hydrateRowActions(result.data.items, includeRequests = false)
                }

                is AppResult.Failure -> {
                    _state.value = NotificationsReducer.onLoadFailed(_state.value, MESSAGE_LOAD_FAILED)
                }
            }
        }
    }

    /**
     * Opens a notification: marks it read, then hands its target to the caller.
     *
     * The mark is fire-and-forget from the UI's point of view — navigation does
     * NOT wait for it. Making someone watch a spinner before a screen opens, to
     * confirm something they can already see happened, is the wrong trade; and
     * the revert on failure is visible when they come back.
     */
    fun onNotificationOpened(notification: Notification) {
        markRead(notification.address)
    }

    fun markRead(address: NotificationAddress) {
        val before = _state.value
        val optimistic = NotificationsReducer.onMarkedRead(before, address)
        // Nothing changed — already read, or not in the loaded page. No request.
        if (optimistic == before) return
        _state.value = optimistic

        publishBadge()

        viewModelScope.launch {
            if (repository.markRead(address) is AppResult.Failure) {
                _state.value = NotificationsReducer.onMarkReadFailed(_state.value, address)
            }
            publishBadge()
        }
    }

    fun markAllRead() {
        val before = _state.value
        if (!before.hasUnread) return
        _state.value = NotificationsReducer.onMarkedAllRead(before)
        publishBadge()

        viewModelScope.launch {
            if (repository.markAllRead() is AppResult.Failure) {
                // The whole prior state, not a recomputation: the count
                // included unread rows never loaded, and nothing in the list
                // can recover that number.
                _state.value = NotificationsReducer.onMarkAllReadFailed(before)
            }
            publishBadge()
        }
    }

    // ── Inline row actions ───────────────────────────────────────────────

    /** Follow back the account that followed you. */
    fun follow(notification: Notification) = runRowAction(notification, RowActionState.Followed) {
        actions.follow(notification.actorUserId)
    }

    /** Accept a message request: the conversation opens and the two connect. */
    fun acceptRequest(notification: Notification) =
        runRowAction(notification, RowActionState.Accepted) {
            actions.acceptRequest(notification.entityId)
        }

    fun declineRequest(notification: Notification) =
        runRowAction(notification, RowActionState.Declined) {
            actions.declineRequest(notification.entityId)
        }

    fun blockRequest(notification: Notification) =
        runRowAction(notification, RowActionState.Blocked) {
            actions.blockRequest(notification.entityId)
        }

    /**
     * One row, one action at a time. Acting on a row also reads it: the user
     * has clearly seen it, and a badge that still counts a request they just
     * accepted is wrong in the direction people notice.
     */
    private fun runRowAction(
        notification: Notification,
        outcome: RowActionState,
        call: suspend () -> AppResult<Unit>,
    ) {
        if (_state.value.rowActions[notification.id] == RowActionState.Busy) return
        _state.value = NotificationsReducer.onActionStarted(_state.value, notification.id)
        markRead(notification.address)

        viewModelScope.launch {
            _state.value = when (call()) {
                is AppResult.Success -> NotificationsReducer.onActionDone(_state.value, notification, outcome)
                is AppResult.Failure -> NotificationsReducer.onActionFailed(_state.value, notification.id)
            }
        }
    }

    /**
     * Fills in what the rows need to offer the right button: which requests
     * are still pending, which followers are already followed back. Both are
     * the server's answers; both are best-effort. A row that cannot learn its
     * state renders without a button rather than with a wrong one.
     */
    private fun hydrateRowActions(items: List<Notification>, includeRequests: Boolean) {
        if (includeRequests && items.any { it.kind == NotificationKind.MessageRequest }) {
            viewModelScope.launch {
                when (val pending = actions.pendingRequestIds()) {
                    is AppResult.Success ->
                        _state.value = NotificationsReducer.onPendingRequests(_state.value, pending.data)
                    is AppResult.Failure -> Unit
                }
            }
        }
        val followers = items
            .filter { it.kind == NotificationKind.Follow && it.actorUserId.isNotBlank() }
            .mapTo(mutableSetOf()) { it.actorUserId }
        if (followers.isNotEmpty()) {
            viewModelScope.launch {
                val following = actions.alreadyFollowing(followers)
                if (following.isNotEmpty()) {
                    _state.value = NotificationsReducer.onFollowing(_state.value, following)
                }
            }
        }
    }

    private suspend fun loadUnreadCount() {
        when (val result = repository.unreadCount()) {
            is AppResult.Success -> {
                _state.value = NotificationsReducer.onUnreadCount(_state.value, result.data)
            }

            // Leave the previous badge. Zeroing it on a failed request would
            // claim "no notifications", which is a lie in the one direction
            // users notice.
            is AppResult.Failure -> Unit
        }
    }

    /**
     * Publishes the current count to the shared badge.
     *
     * Called after every read-state change, optimistic or confirmed, so the
     * feed and the inbox can never disagree about the number.
     */
    private fun publishBadge() {
        badge.set(_state.value.unreadCount)
    }

    private companion object {
        const val MESSAGE_LOAD_FAILED = "Couldn't load your notifications."
    }
}
