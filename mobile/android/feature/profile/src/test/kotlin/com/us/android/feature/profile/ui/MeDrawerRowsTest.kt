package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.profile.navigation.MomentumHeaderDestinations
import org.junit.Test

/**
 * The Me tab's ☰ drawer (founder, 2026-10-17), which replaced the ⋮ More
 * sheet: "Your activity" (Liked, Saved, Offline videos, Messages), "Creator
 * tools" (Creator Hub, Upload), then one "Settings & privacy" row under a
 * divider.
 *
 * What this protects: the order, rows existing only where the page was
 * given somewhere to send them, and nothing at all on someone else's
 * profile (whose ⋮ menu is [ProfileMenuRowsTest]'s).
 */
class MeDrawerRowsTest {

    private val everyDoor = MeDrawerDoors(
        grid = true,
        followedHashtags = true,
        offline = true,
        creditRequests = true,
        myMemberships = true,
        messages = true,
        creatorHub = true,
        memberships = true,
        upload = true,
        settings = true,
    )

    @Test
    fun `with every door the drawer is the two groups then Settings and privacy, in order`() {
        val sections = meDrawerSections(isOwnProfile = true, doors = everyDoor)

        assertThat(sections.map { it.group })
            .containsExactly(MeDrawerGroup.ACTIVITY, MeDrawerGroup.CREATOR_TOOLS, MeDrawerGroup.SETTINGS)
            .inOrder()
        assertThat(sections.flatMap { it.rows })
            .containsExactly(
                MeDrawerRow.LIKED,
                MeDrawerRow.SAVED,
                MeDrawerRow.FOLLOWED_HASHTAGS,
                MeDrawerRow.OFFLINE,
                MeDrawerRow.CREDIT_REQUESTS,
                MeDrawerRow.MY_MEMBERSHIPS,
                MeDrawerRow.MESSAGES,
                MeDrawerRow.CREATOR_HUB,
                MeDrawerRow.MEMBERSHIPS,
                MeDrawerRow.UPLOAD,
                MeDrawerRow.SETTINGS,
            )
            .inOrder()
    }

    @Test
    fun `the headings and labels read as the founder asked`() {
        assertThat(MeDrawerGroup.ACTIVITY.heading).isEqualTo("Your activity")
        assertThat(MeDrawerGroup.CREATOR_TOOLS.heading).isEqualTo("Creator tools")
        // The Settings row stands alone under the divider, with no heading.
        assertThat(MeDrawerGroup.SETTINGS.heading).isNull()
        assertThat(MeDrawerRow.entries.map { it.label }).containsExactly(
            "Liked",
            "Saved",
            "Followed hashtags",
            "Offline videos",
            "Credit requests",
            "My memberships",
            "Messages",
            "Creator Hub",
            "Memberships",
            "Upload",
            "Settings & privacy",
        ).inOrder()
    }

    @Test
    fun `someone else's profile gets no drawer at all`() {
        assertThat(meDrawerSections(isOwnProfile = false, doors = everyDoor)).isEmpty()
    }

    @Test
    fun `a row the page has nowhere to send is not drawn`() {
        val rows = meDrawerSections(
            isOwnProfile = true,
            doors = everyDoor.copy(offline = false, creatorHub = false),
        ).flatMap { it.rows }

        assertThat(rows).doesNotContain(MeDrawerRow.OFFLINE)
        assertThat(rows).doesNotContain(MeDrawerRow.CREATOR_HUB)
        assertThat(rows).contains(MeDrawerRow.UPLOAD)
    }

    @Test
    fun `Liked and Saved wait for the own grid to be on screen`() {
        val rows = meDrawerSections(isOwnProfile = true, doors = everyDoor.copy(grid = false)).flatMap { it.rows }

        assertThat(rows).containsNoneOf(MeDrawerRow.LIKED, MeDrawerRow.SAVED)
        assertThat(rows.first()).isEqualTo(MeDrawerRow.FOLLOWED_HASHTAGS)
    }

    @Test
    fun `a group with nothing to offer is left out, heading and all`() {
        val sections = meDrawerSections(
            isOwnProfile = true,
            doors = MeDrawerDoors(messages = true, settings = true),
        )

        assertThat(sections.map { it.group }).containsExactly(MeDrawerGroup.ACTIVITY, MeDrawerGroup.SETTINGS).inOrder()
        assertThat(sections.flatMap { it.rows }).containsExactly(MeDrawerRow.MESSAGES, MeDrawerRow.SETTINGS).inOrder()
        assertThat(meDrawerSections(isOwnProfile = true, doors = MeDrawerDoors())).isEmpty()
    }

    @Test
    fun `Liked and Saved open grid tabs in place, every other row leaves the profile`() {
        assertThat(MeDrawerRow.LIKED.gridTab()).isEqualTo(ProfileGridTab.LIKED)
        assertThat(MeDrawerRow.SAVED.gridTab()).isEqualTo(ProfileGridTab.SAVED)
        assertThat(MeDrawerRow.entries.filter { it.gridTab() == null })
            .containsExactly(
                MeDrawerRow.FOLLOWED_HASHTAGS,
                MeDrawerRow.OFFLINE,
                MeDrawerRow.CREDIT_REQUESTS,
                MeDrawerRow.MY_MEMBERSHIPS,
                MeDrawerRow.MESSAGES,
                MeDrawerRow.CREATOR_HUB,
                MeDrawerRow.MEMBERSHIPS,
                MeDrawerRow.UPLOAD,
                MeDrawerRow.SETTINGS,
            )
    }

    /** Followed hashtags (2026-10-24): under Your activity, only where `:app` gave it the list to open. */
    @Test
    fun `the followed hashtags row follows its door`() {
        val without = meDrawerSections(isOwnProfile = true, doors = everyDoor.copy(followedHashtags = false)).flatMap { it.rows }

        assertThat(without).doesNotContain(MeDrawerRow.FOLLOWED_HASHTAGS)
        assertThat(MeDrawerRow.FOLLOWED_HASHTAGS.group).isEqualTo(MeDrawerGroup.ACTIVITY)
        assertThat(MeDrawerRow.FOLLOWED_HASHTAGS.gridTab()).isNull()
    }

    /** Paid memberships (2026-10-23): each row only where `:app` gave it somewhere to go. */
    @Test
    fun `the membership rows follow their doors`() {
        val without = meDrawerSections(
            isOwnProfile = true,
            doors = everyDoor.copy(myMemberships = false, memberships = false),
        ).flatMap { it.rows }
        assertThat(without).containsNoneOf(MeDrawerRow.MY_MEMBERSHIPS, MeDrawerRow.MEMBERSHIPS)
        assertThat(MeDrawerRow.MY_MEMBERSHIPS.group).isEqualTo(MeDrawerGroup.ACTIVITY)
        assertThat(MeDrawerRow.MEMBERSHIPS.group).isEqualTo(MeDrawerGroup.CREATOR_TOOLS)
    }

    @Test
    fun `the doors follow what the page was given`() {
        val bare = ProfileDestinations(onOpenFollowers = {}, onOpenFollowing = {})
        assertThat(meDrawerDoors(ProfileUiState.Loading, bare)).isEqualTo(MeDrawerDoors())

        val meTab = bare.copy(
            onOpenSettings = {},
            header = MomentumHeaderDestinations(onOpenSearch = {}, onOpenMessages = {}, onOpenNotifications = {}),
            onOpenCreatorHub = {},
            onOpenOffline = {},
            onOpenCreditRequests = {},
            onOpenFollowedHashtags = {},
            onOpenMyMemberships = {},
            onOpenMemberships = {},
            onOpenUpload = {},
        )
        // Still loading: no grid yet, so Liked and Saved are not offered.
        assertThat(meDrawerDoors(ProfileUiState.Loading, meTab))
            .isEqualTo(everyDoor.copy(grid = false))
    }
}
