package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.notifications.data.NotificationPage
import kotlinx.coroutines.runBlocking
import org.junit.Test

/** The chips' wire names and [InboxPaging]'s rule, as tables. */
class ActivityFiltersTest {

    private fun row(id: String) = Notification(
        id = id,
        bucket = 202610,
        ts = "ts-$id",
        kind = NotificationKind.Reaction,
        actorUserId = "actor",
        entityType = "post",
        entityId = "post-1",
        target = NotificationTarget.Post("post-1"),
        isRead = false,
        createdAt = "2026-10-14T10:00:00Z",
    )

    private fun page(vararg ids: String, next: String? = null): AppResult<NotificationPage> =
        AppResult.Success(NotificationPage(ids.map { row(it) }, next))

    /** Answers its pages in order and records each cursor asked for. */
    private class Pages(vararg pages: AppResult<NotificationPage>) {
        private val queue = ArrayDeque(pages.toList())
        val asked = mutableListOf<String?>()

        fun fetch(cursor: String?): AppResult<NotificationPage> {
            asked += cursor
            return queue.removeFirst()
        }
    }

    @Test
    fun `the chips map onto the server's categories, All onto none`() {
        assertThat(ActivityFilter.entries.map { it.label to it.category }).containsExactly(
            "All" to null,
            "Likes" to "likes",
            "Comments" to "comments",
            "Mentions" to "mentions",
            "Followers" to "followers",
        ).inOrder()
    }

    @Test
    fun `a page with rows is one request`() = runBlocking {
        val pages = Pages(page("a", next = "c1"))
        val result = InboxPaging.fill(cursor = null) { pages.fetch(it) }

        assertThat(pages.asked).containsExactly(null)
        assertThat(result).isEqualTo(page("a", next = "c1"))
    }

    @Test
    fun `empty pages with a cursor are paged through until rows appear`() = runBlocking {
        val pages = Pages(page(next = "c1"), page(next = "c2"), page("a", next = "c3"))
        val result = InboxPaging.fill(cursor = null) { pages.fetch(it) }

        assertThat(pages.asked).containsExactly(null, "c1", "c2").inOrder()
        assertThat(result).isEqualTo(page("a", next = "c3"))
    }

    @Test
    fun `paging through empty pages stops at the cap, keeping the cursor`() = runBlocking {
        val pages = Pages(page(next = "c1"), page(next = "c2"), page(next = "c3"), page("never"))
        val result = InboxPaging.fill(cursor = "c0", maxRequests = 3) { pages.fetch(it) }

        assertThat(pages.asked).containsExactly("c0", "c1", "c2").inOrder()
        assertThat(result).isEqualTo(page(next = "c3"))
    }

    @Test
    fun `a missing cursor is the end, even when the page was empty`() = runBlocking {
        val pages = Pages(page(next = "c1"), page())
        val result = InboxPaging.fill(cursor = null) { pages.fetch(it) }

        assertThat(pages.asked).containsExactly(null, "c1").inOrder()
        assertThat(result).isEqualTo(page())
    }

    @Test
    fun `a failure is the failure`() = runBlocking {
        val failure = AppResult.Failure(AppError.NoNetwork())
        val pages = Pages(page(next = "c1"), failure)
        val result = InboxPaging.fill(cursor = null) { pages.fetch(it) }

        assertThat(result).isEqualTo(failure)
    }
}
