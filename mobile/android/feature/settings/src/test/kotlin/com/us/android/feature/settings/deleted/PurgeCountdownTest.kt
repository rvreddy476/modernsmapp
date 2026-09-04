package com.us.android.feature.settings.deleted

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Instant

/**
 * The line under a deleted post — "Deleted 2h ago · Permanently removed in
 * 28 days" — pinned against a fixed clock so the rounding rule (days round
 * UP, never promising a day the viewer does not have) cannot drift.
 */
class PurgeCountdownTest {

    private val now: Instant = Instant.parse("2026-09-04T12:00:00Z")

    @Test
    fun `the row reads deleted-age then the purge countdown`() {
        val line = deletedRowSubtitle(
            deletedAt = "2026-09-04T10:00:00Z",
            purgeAt = "2026-10-02T06:00:00Z",
            now = now,
        )

        assertThat(line).isEqualTo("Deleted 2h ago · Permanently removed in 28 days")
    }

    @Test
    fun `days round up so a partial day still counts`() {
        assertThat(purgeLabel("2026-10-04T12:00:00Z", now)).isEqualTo("Permanently removed in 30 days")
        assertThat(purgeLabel("2026-10-04T11:59:59Z", now)).isEqualTo("Permanently removed in 30 days")
        assertThat(purgeLabel("2026-09-05T12:00:00Z", now)).isEqualTo("Permanently removed in 1 day")
        assertThat(purgeLabel("2026-09-05T13:00:00Z", now)).isEqualTo("Permanently removed in 2 days")
    }

    @Test
    fun `under a day it counts hours, under an hour it says so`() {
        assertThat(purgeLabel("2026-09-04T17:30:00Z", now)).isEqualTo("Permanently removed in 6 hours")
        assertThat(purgeLabel("2026-09-04T13:00:00Z", now)).isEqualTo("Permanently removed in 1 hour")
        assertThat(purgeLabel("2026-09-04T12:20:00Z", now)).isEqualTo("Permanently removed in less than an hour")
    }

    @Test
    fun `a purge moment already past is being removed`() {
        assertThat(purgeLabel("2026-09-04T11:00:00Z", now)).isEqualTo("Being permanently removed")
        assertThat(purgeLabel("2026-09-04T12:00:00Z", now)).isEqualTo("Being permanently removed")
    }

    @Test
    fun `the deleted age reuses the feed's own age string`() {
        assertThat(deletedLabel("2026-09-04T11:59:40Z", now)).isEqualTo("Deleted just now")
        assertThat(deletedLabel("2026-09-04T11:45:00Z", now)).isEqualTo("Deleted 15m ago")
        assertThat(deletedLabel("2026-09-01T12:00:00Z", now)).isEqualTo("Deleted 3d ago")
        // Past a week the feed shows a date, and "12 Aug ago" would be wrong.
        assertThat(deletedLabel("2026-08-12T12:00:00Z", now)).startsWith("Deleted 12 Aug")
        assertThat(deletedLabel("2026-08-12T12:00:00Z", now)).doesNotContain("ago")
    }

    @Test
    fun `a missing or malformed timestamp never shows a raw value`() {
        assertThat(deletedRowSubtitle(deletedAt = "", purgeAt = "", now = now)).isEqualTo("Deleted")
        assertThat(deletedRowSubtitle(deletedAt = "yesterday", purgeAt = "soon", now = now)).isEqualTo("Deleted")
        assertThat(deletedRowSubtitle(deletedAt = "2026-09-04T10:00:00Z", purgeAt = "", now = now))
            .isEqualTo("Deleted 2h ago")
    }
}
