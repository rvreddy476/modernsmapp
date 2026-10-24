package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The More menu on a profile (audit T-03): Report on someone else's, nothing
 * at all on the viewer's own — the screen draws no More control when the list
 * is empty, so "no Report for self" is this table.
 */
class ProfileMenuRowsTest {

    @Test
    fun `someone else's profile offers Report`() {
        assertThat(profileMenuRows(isOwnProfile = false)).contains(ProfileMenuRow.REPORT)
    }

    @Test
    fun `the viewer's own profile has no Report, and no menu`() {
        assertThat(profileMenuRows(isOwnProfile = true)).doesNotContain(ProfileMenuRow.REPORT)
        assertThat(profileMenuRows(isOwnProfile = true)).isEmpty()
    }

    @Test
    fun `the rows are in alphabetical order and Report reads as destructive`() {
        assertThat(profileMenuRows(isOwnProfile = false).map { it.label })
            .isInOrder(String.CASE_INSENSITIVE_ORDER)
        assertThat(ProfileMenuRow.REPORT.label).isEqualTo("Report")
        assertThat(ProfileMenuRow.REPORT.destructive).isTrue()
    }
}

/** The account rows (2026-10-24, the web's profileParity.ts `profileMenuRows`). */
class ProfileMenuAccountRowsTest {

    @Test
    fun `a muted account offers Unmute instead of Mute`() {
        val rows = profileMenuRows(isOwnProfile = false, isMuted = true)

        assertThat(rows).contains(ProfileMenuRow.UNMUTE)
        assertThat(rows).doesNotContain(ProfileMenuRow.MUTE)
    }

    @Test
    fun `a blocked account offers Unblock and no mute row at all`() {
        val rows = profileMenuRows(isOwnProfile = false, isMuted = true, isBlocked = true)

        assertThat(rows).containsExactly(ProfileMenuRow.REPORT, ProfileMenuRow.UNBLOCK).inOrder()
    }

    @Test
    fun `Block reads as destructive and Unblock does not`() {
        assertThat(ProfileMenuRow.BLOCK.destructive).isTrue()
        assertThat(ProfileMenuRow.UNBLOCK.destructive).isFalse()
        assertThat(ProfileMenuRow.MUTE.destructive).isFalse()
    }

    @Test
    fun `the profile link carries the share tail, and a handle-less profile has none`() {
        assertThat(profileLink("ada")).matches("https://cleestudio.com/u/ada\\?ref=[a-z0-9]{10}&utm_source=share")
        assertThat(profileLink("")).isNull()
    }
}
