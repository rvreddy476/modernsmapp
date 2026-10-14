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
