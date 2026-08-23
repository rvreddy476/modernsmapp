package com.us.android.core.notifications.data

import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The unread count, shared by every surface that shows it — Slice D.
 *
 * ## WHY THIS IS A SINGLETON AND NOT PER-SCREEN STATE
 *
 * Two surfaces render this number: the feed's top-bar badge and the inbox
 * itself. If each fetched and stored its own copy, marking a notification read
 * inside the inbox would leave the feed's badge stale until the feed happened
 * to refetch — the user would return to the feed and still see "3" after
 * clearing everything, which reads as the app being broken.
 *
 * One holder, written by whoever learns a newer value, observed by both.
 *
 * ## IT IS A CACHE, NOT AN AUTHORITY
 *
 * The server computes the real count across the whole inbox. This holds the
 * most recent answer plus any optimistic adjustment made since. Every
 * [refresh] overwrites it with the server's value rather than reconciling,
 * because a client that has drifted should be corrected, not negotiated with.
 */
@Singleton
class UnreadBadge @Inject constructor(
    private val repository: NotificationsRepository,
) {

    private val _count = MutableStateFlow(0)
    val count: StateFlow<Int> = _count.asStateFlow()

    /**
     * Re-reads the server's count.
     *
     * A failure leaves the previous value. Zeroing the badge because a request
     * failed would claim "nothing new", which is wrong in the one direction
     * users actually notice.
     */
    suspend fun refresh() {
        when (val result = repository.unreadCount()) {
            is AppResult.Success -> set(result.data)
            is AppResult.Failure -> Unit
        }
    }

    /** Publishes a count the inbox already knows, including optimistic ones. */
    fun set(count: Int) {
        _count.value = count.coerceAtLeast(0)
    }
}
