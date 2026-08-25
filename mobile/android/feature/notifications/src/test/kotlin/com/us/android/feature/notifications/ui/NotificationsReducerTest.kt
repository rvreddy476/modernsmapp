package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationAddress
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * The inbox's read-state and paging rules — Slice D.
 *
 * ## WHY THESE ARE RULES AND NOT PLUMBING
 *
 * Marking a notification read touches two things at once: the row, and a count
 * the server owns and the client only mirrors. Every way of getting that wrong
 * is visible to the user and looks like a broken app:
 *
 *  - decrementing on an already-read row drives the badge negative;
 *  - reverting a failure only on the row leaves the badge permanently short;
 *  - recomputing the count from the loaded page loses every unread row that was
 *    never fetched.
 *
 * So the arithmetic lives in a pure object and is tabled here.
 */
class NotificationsReducerTest {

    private fun notification(
        id: String,
        bucket: Int = 202608,
        ts: String = "ts-$id",
        isRead: Boolean = false,
    ) = Notification(
        id = id,
        bucket = bucket,
        ts = ts,
        kind = NotificationKind.Comment,
        actorUserId = "actor",
        entityType = "post",
        entityId = "post-1",
        target = NotificationTarget.Post("post-1"),
        isRead = isRead,
        createdAt = "2026-08-22T17:10:21.526Z",
    )

    private fun state(
        items: List<Notification> = emptyList(),
        unread: Int = 0,
        cursor: String? = null,
    ) = NotificationsUiState(items = items, unreadCount = unread, nextCursor = cursor)

    // ── Read state ──────────────────────────────────────────────────────

    @Test
    fun `marking an unread row read decrements the count`() {
        val row = notification("n1")
        val result = NotificationsReducer.onMarkedRead(state(listOf(row), unread = 3), row.address)

        assertThat(result.items.single().isRead).isTrue()
        assertThat(result.unreadCount).isEqualTo(2)
    }

    /**
     * The badge cannot drift on a repeated tap.
     *
     * Without this guard, tapping the same notification twice decrements twice
     * and the badge eventually shows a negative number — the kind of bug users
     * screenshot.
     */
    @Test
    fun `marking an already-read row changes nothing`() {
        val row = notification("n1", isRead = true)
        val before = state(listOf(row), unread = 3)

        assertThat(NotificationsReducer.onMarkedRead(before, row.address)).isEqualTo(before)
    }

    /** A row outside the loaded page is not ours to mark. */
    @Test
    fun `marking an unknown address changes nothing`() {
        val before = state(listOf(notification("n1")), unread = 1)

        val result = NotificationsReducer.onMarkedRead(before, NotificationAddress(1, "nope"))

        assertThat(result).isEqualTo(before)
    }

    /**
     * The count clamps at zero.
     *
     * Client and server can disagree — an aggregation window can flush between
     * a fetch and a tap. Clamping is cheaper than being wrong in public.
     */
    @Test
    fun `the count never goes below zero`() {
        val row = notification("n1")

        val result = NotificationsReducer.onMarkedRead(state(listOf(row), unread = 0), row.address)

        assertThat(result.unreadCount).isEqualTo(0)
    }

    /** A failed mark restores BOTH halves, not just the row. */
    @Test
    fun `a failed mark-read restores the row and the count together`() {
        val row = notification("n1")
        val optimistic = NotificationsReducer.onMarkedRead(state(listOf(row), unread = 3), row.address)

        val reverted = NotificationsReducer.onMarkReadFailed(optimistic, row.address)

        assertThat(reverted.items.single().isRead).isFalse()
        assertThat(reverted.unreadCount).isEqualTo(3)
    }

    /** Reverting something that is not marked read must not inflate the count. */
    @Test
    fun `reverting an unread row changes nothing`() {
        val row = notification("n1")
        val before = state(listOf(row), unread = 3)

        assertThat(NotificationsReducer.onMarkReadFailed(before, row.address)).isEqualTo(before)
    }

    @Test
    fun `mark-all-read reads every row and zeroes the count`() {
        val before = state(
            listOf(notification("n1"), notification("n2", isRead = true), notification("n3")),
            unread = 12,
        )

        val result = NotificationsReducer.onMarkedAllRead(before)

        assertThat(result.items.all { it.isRead }).isTrue()
        assertThat(result.unreadCount).isEqualTo(0)
    }

    /**
     * A failed mark-all restores the ORIGINAL count, not a recomputed one.
     *
     * The count included unread notifications that were never loaded — here,
     * 12 unread behind a 3-row page. Recomputing from the list would silently
     * reset the badge to 2 and lose nine of them.
     */
    @Test
    fun `a failed mark-all restores the count the list cannot recover`() {
        val before = state(
            listOf(notification("n1"), notification("n2", isRead = true), notification("n3")),
            unread = 12,
        )
        val optimistic = NotificationsReducer.onMarkedAllRead(before)

        val reverted = NotificationsReducer.onMarkAllReadFailed(before)

        assertThat(optimistic.unreadCount).isEqualTo(0)
        assertThat(reverted).isEqualTo(before)
        assertThat(reverted.unreadCount).isEqualTo(12)
    }

    @Test
    fun `the server count replaces the local one and clamps`() {
        assertThat(NotificationsReducer.onUnreadCount(state(unread = 5), 9).unreadCount).isEqualTo(9)
        assertThat(NotificationsReducer.onUnreadCount(state(unread = 5), -1).unreadCount).isEqualTo(0)
    }

    // ── Paging ──────────────────────────────────────────────────────────

    @Test
    fun `the first page replaces rather than appends`() {
        val before = state(listOf(notification("old")), cursor = "c1")

        val result = NotificationsReducer.onFirstPage(before, listOf(notification("new")), null)

        assertThat(result.items.map { it.id }).containsExactly("new")
        assertThat(result.nextCursor).isNull()
    }

    /**
     * A later page appends and de-duplicates.
     *
     * The inbox is time-ordered, so a notification arriving between two page
     * fetches shifts every subsequent row and the same id genuinely reappears.
     * Compose throws on duplicate keys in a keyed list, so this is a crash, not
     * a cosmetic issue.
     */
    @Test
    fun `a later page appends and drops rows already on screen`() {
        val before = state(listOf(notification("n1"), notification("n2")), cursor = "c1")

        val result = NotificationsReducer.onNextPage(
            before,
            listOf(notification("n2"), notification("n3")),
            "c2",
        )

        assertThat(result.items.map { it.id }).containsExactly("n1", "n2", "n3").inOrder()
        assertThat(result.nextCursor).isEqualTo("c2")
        assertThat(result.isLoadingMore).isFalse()
    }

    @Test
    fun `load-more is refused when there is no next page`() {
        val before = state(listOf(notification("n1")), cursor = null)

        assertThat(NotificationsReducer.onLoadMoreStarted(before)).isEqualTo(before)
    }

    @Test
    fun `load-more is refused while one is already running`() {
        val before = state(listOf(notification("n1")), cursor = "c1").copy(isLoadingMore = true)

        assertThat(NotificationsReducer.onLoadMoreStarted(before)).isEqualTo(before)
    }

    /** An empty inbox shows the empty state, not a spinner and not an error. */
    @Test
    fun `an empty first page is the empty state`() {
        val result = NotificationsReducer.onFirstPage(state().copy(isLoading = true), emptyList(), null)

        assertThat(result.isEmpty).isTrue()
        assertThat(result.isLoading).isFalse()
        assertThat(result.error).isNull()
    }

    /**
     * A failed FIRST page shows an error; a failed LATER page does not.
     *
     * An error banner over a working list reads as though the whole inbox
     * broke, when in fact the user has content and only the next page failed.
     */
    @Test
    fun `only a failed first page surfaces an error`() {
        val empty = NotificationsReducer.onLoadFailed(state(), "boom")
        assertThat(empty.error).isEqualTo("boom")

        val populated = NotificationsReducer.onLoadFailed(state(listOf(notification("n1"))), "boom")
        assertThat(populated.error).isNull()
        assertThat(populated.items).hasSize(1)
    }

    /** A refresh over existing rows is a refresh, not a full-screen load. */
    @Test
    fun `loading with rows on screen refreshes rather than blanking`() {
        val result = NotificationsReducer.onLoadStarted(state(listOf(notification("n1"))))

        assertThat(result.isRefreshing).isTrue()
        assertThat(result.isLoading).isFalse()
    }

    @Test
    fun `loading with an empty list is a full-screen load`() {
        val result = NotificationsReducer.onLoadStarted(state())

        assertThat(result.isLoading).isTrue()
        assertThat(result.isRefreshing).isFalse()
    }
}
