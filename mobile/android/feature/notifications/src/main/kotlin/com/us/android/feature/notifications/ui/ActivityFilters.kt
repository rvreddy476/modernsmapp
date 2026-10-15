package com.us.android.feature.notifications.ui

import com.us.android.core.common.result.AppResult
import com.us.android.core.model.Notification
import com.us.android.core.notifications.data.NotificationPage

/**
 * The Activity chips (T-11, 2026-10-14), each mapped onto notification-
 * service's `?category=`. The server decides which types a tab holds — from
 * the same categories the notification settings use — so the client never
 * keeps its own list of types per chip.
 */
enum class ActivityFilter(val label: String, val category: String?, val emptyTitle: String, val emptyDetail: String) {
    All(
        label = "All",
        category = null,
        emptyTitle = "Nothing yet",
        emptyDetail = "Reactions, comments, follows and message requests will show up here.",
    ),
    Likes(
        label = "Likes",
        category = "likes",
        emptyTitle = "No likes yet",
        emptyDetail = "When someone likes your posts, you'll see it here.",
    ),
    Comments(
        label = "Comments",
        category = "comments",
        emptyTitle = "No comments yet",
        emptyDetail = "Comments and replies on your posts will show up here.",
    ),
    Mentions(
        label = "Mentions",
        category = "mentions",
        emptyTitle = "No mentions yet",
        emptyDetail = "When someone mentions you, you'll see it here.",
    ),
    Followers(
        label = "Followers",
        category = "followers",
        emptyTitle = "No followers yet",
        emptyDetail = "New followers and follow requests will show up here.",
    ),
}

/**
 * Paging a filtered inbox. A filtered page is the unfiltered page with the
 * other kinds taken out, so it can be short or EMPTY while still carrying
 * `next_cursor`; only a missing cursor means the end. An empty page with a
 * cursor is therefore not "nothing here" — the next page is fetched — but
 * one user action (opening a chip, reaching the bottom, "Show more") costs
 * at most [MAX_REQUESTS] requests, so a sparse tab cannot walk the whole
 * inbox in one go. Pure, so the rule is a table test.
 */
object InboxPaging {
    const val MAX_REQUESTS = 3

    /**
     * Fetches from [cursor] until a page has rows, the cursor runs out, or
     * [maxRequests] have been made. The answer is every row found and the
     * cursor to continue from — null only when the server said "the end".
     * A failure is the failure: the loop only goes on while nothing has been
     * found, so there are no rows to keep, and the action that retries
     * starts again from [cursor].
     */
    suspend fun fill(
        cursor: String?,
        maxRequests: Int = MAX_REQUESTS,
        fetch: suspend (cursor: String?) -> AppResult<NotificationPage>,
    ): AppResult<NotificationPage> {
        val found = mutableListOf<Notification>()
        var next = cursor
        var requests = 0
        do {
            when (val result = fetch(next)) {
                is AppResult.Failure -> return result
                is AppResult.Success -> {
                    found += result.data.items
                    next = result.data.nextCursor
                }
            }
            requests++
        } while (found.isEmpty() && next != null && requests < maxRequests)
        return AppResult.Success(NotificationPage(found, next))
    }
}
