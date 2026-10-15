package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Share profile (T-21): the owner shares from beside Edit profile, a visitor
 * from the More menu, and both hand out the public `/u/<username>` address.
 */
class ProfileShareTest {

    @Test
    fun `the shared text is the name, then the public profile address on its own line`() {
        assertThat(profileShareText("Ada Lovelace", "ada"))
            .isEqualTo("Ada Lovelace\n\nhttps://cleestudio.com/u/ada")
    }

    @Test
    fun `a blank name shares the address alone`() {
        assertThat(profileShareText("  ", "ada")).isEqualTo("https://cleestudio.com/u/ada")
    }

    @Test
    fun `a profile without a handle has nothing to share`() {
        assertThat(profileShareText("Ada", "")).isNull()
    }

    @Test
    fun `a visitor's menu offers Share profile beside Report, in alphabetical order`() {
        val rows = profileMenuRows(isOwnProfile = false, canShare = true)

        assertThat(rows).containsExactly(ProfileMenuRow.REPORT, ProfileMenuRow.SHARE).inOrder()
        assertThat(ProfileMenuRow.SHARE.destructive).isFalse()
    }

    @Test
    fun `no Share row when the profile has no address`() {
        assertThat(profileMenuRows(isOwnProfile = false, canShare = false)).containsExactly(ProfileMenuRow.REPORT)
    }

    /** The owner's Share is a button beside Edit profile, so their own profile still has no menu. */
    @Test
    fun `the owner's own profile keeps no More menu`() {
        assertThat(profileMenuRows(isOwnProfile = true, canShare = true)).isEmpty()
    }
}
