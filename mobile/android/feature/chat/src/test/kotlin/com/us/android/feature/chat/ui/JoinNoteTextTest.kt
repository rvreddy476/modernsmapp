package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.JoinMethod
import com.us.android.core.chat.data.MyJoin
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZoneOffset
import java.util.Locale
import org.junit.Test

/** The "you joined" note at the top of a group chat (founder, 2026-10-22). */
class JoinNoteTextTest {
    private val today = LocalDate.of(2026, 10, 22)
    private fun join(method: JoinMethod, by: String? = null) = MyJoin("2026-10-22T09:30:00Z", method, by)

    @Test
    fun `each way in`() {
        assertThat(joinNoteText(join(JoinMethod.InviteLink), null, "22 Oct"))
            .isEqualTo("You joined using this group's invite link · 22 Oct")
        assertThat(joinNoteText(join(JoinMethod.Added, "u1"), "Asha", "22 Oct")).isEqualTo("Asha added you · 22 Oct")
        assertThat(joinNoteText(join(JoinMethod.Invitation, "u1"), "Asha", "22 Oct")).isEqualTo("You joined · 22 Oct")
    }

    @Test
    fun `an unknown adder is Someone`() {
        assertThat(joinNoteText(join(JoinMethod.Added), null, "22 Oct")).isEqualTo("Someone added you · 22 Oct")
        assertThat(joinNoteText(join(JoinMethod.Added, "u1"), "  ", "22 Oct")).isEqualTo("Someone added you · 22 Oct")
    }

    @Test
    fun `no date drops the date, not the note`() {
        assertThat(joinNoteText(join(JoinMethod.Invitation), null, "")).isEqualTo("You joined")
        assertThat(joinNoteDate("yesterday", Locale.UK, ZoneOffset.UTC, today)).isEmpty()
    }

    @Test
    fun `the date is the locale pattern, in the viewer's zone, with the year only for another year`() {
        assertThat(joinNoteDate("2026-10-22T09:30:00Z", Locale.UK, ZoneOffset.UTC, today)).isEqualTo("22 Oct")
        assertThat(joinNoteDate("2026-10-22T09:30:00Z", Locale.US, ZoneOffset.UTC, today, dayPattern = "MMM d"))
            .isEqualTo("Oct 22")
        // 21:00 UTC on the 22nd is already the 23rd in Kolkata.
        assertThat(joinNoteDate("2026-10-22T21:00:00Z", Locale.UK, ZoneId.of("Asia/Kolkata"), today)).isEqualTo("23 Oct")
        assertThat(joinNoteDate("2025-10-22T09:30:00Z", Locale.UK, ZoneOffset.UTC, today)).isEqualTo("22 Oct 2025")
    }

    @Test
    fun `the second line`() {
        assertThat(JOIN_NOTE_HISTORY_TEXT).isEqualTo("Messages sent before you joined aren't shown.")
    }
}
