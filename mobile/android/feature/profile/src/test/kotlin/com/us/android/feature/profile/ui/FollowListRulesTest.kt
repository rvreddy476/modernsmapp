package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.PersonalProfile
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.ProfileRelationship
import com.us.android.feature.profile.navigation.FollowListRoute
import org.junit.Test

/** The pill a row wears, when the counts open the lists, and what the counts open. */
class FollowListRulesTest {

    private fun pill(
        edge: FollowStatus?,
        tab: FollowListTab = FollowListTab.FOLLOWERS,
        own: Boolean = true,
        person: String = "a",
        viewer: String = "me",
    ) = followPillFor(viewer, person, edge, tab, own)

    @Test
    fun `the viewer's own row and a signed-out viewer get no pill`() {
        assertThat(pill(FollowStatus.NONE, person = "me")).isNull()
        assertThat(pill(FollowStatus.NONE, viewer = "")).isNull()
    }

    @Test
    fun `an unknown edge gets no pill rather than a guess`() {
        assertThat(pill(edge = null)).isNull()
    }

    @Test
    fun `following and requested read the same on every tab`() {
        FollowListTab.entries.forEach { tab ->
            listOf(true, false).forEach { own ->
                assertThat(pill(FollowStatus.FOLLOWING, tab, own)).isEqualTo(FollowPill.FOLLOWING)
                assertThat(pill(FollowStatus.REQUESTED, tab, own)).isEqualTo(FollowPill.REQUESTED)
            }
        }
    }

    @Test
    fun `Follow back only on the viewer's own Followers tab`() {
        assertThat(pill(FollowStatus.NONE, FollowListTab.FOLLOWERS, own = true)).isEqualTo(FollowPill.FOLLOW_BACK)
        assertThat(pill(FollowStatus.NONE, FollowListTab.FOLLOWERS, own = false)).isEqualTo(FollowPill.FOLLOW)
        assertThat(pill(FollowStatus.NONE, FollowListTab.FOLLOWING, own = true)).isEqualTo(FollowPill.FOLLOW)
        assertThat(pill(FollowStatus.NONE, FollowListTab.FOLLOWING, own = false)).isEqualTo(FollowPill.FOLLOW)
    }

    @Test
    fun `the pills that start an edge are filled, the ones that undo it are not`() {
        assertThat(FollowPill.FOLLOW.filled).isTrue()
        assertThat(FollowPill.FOLLOW_BACK.filled).isTrue()
        assertThat(FollowPill.FOLLOWING.filled).isFalse()
        assertThat(FollowPill.REQUESTED.filled).isFalse()
    }

    @Test
    fun `a tab's wire name round-trips, and anything else opens Followers`() {
        FollowListTab.entries.forEach { assertThat(FollowListTab.fromWire(it.wire)).isEqualTo(it) }
        assertThat(FollowListTab.fromWire(null)).isEqualTo(FollowListTab.FOLLOWERS)
        assertThat(FollowListTab.fromWire("friends")).isEqualTo(FollowListTab.FOLLOWERS)
    }

    @Test
    fun `the title is the handle`() {
        assertThat(followListTitle("ada")).isEqualTo("@ada")
        assertThat(followListTitle("")).isEqualTo("Connections")
    }

    @Test
    fun `a count opens its own tab with the handle`() {
        assertThat(followListRoute(publicProfile, FollowListTab.FOLLOWING))
            .isEqualTo(FollowListRoute(userId = "u1", handle = "ada", tab = "following"))
    }

    // ── canOpenConnections ───────────────────────────────────────────────

    private fun content(profile: Profile, relationship: ProfileRelationship = ProfileRelationship()) =
        ProfileUiState.Content(profile = profile, stats = null, relationship = relationship)

    @Test
    fun `the viewer's own lists always open, however private`() {
        assertThat(content(ownProfile.copy(isPrivate = true)).canOpenConnections).isTrue()
    }

    @Test
    fun `a public account's lists open`() {
        assertThat(content(publicProfile).canOpenConnections).isTrue()
    }

    @Test
    fun `a private account opens only for a follower`() {
        val private = publicProfile.copy(isPrivate = true)
        assertThat(content(private).canOpenConnections).isFalse()
        assertThat(
            content(private, ProfileRelationship(followStatus = FollowStatus.REQUESTED)).canOpenConnections,
        ).isFalse()
        assertThat(
            content(private, ProfileRelationship(isFollowing = true, followStatus = FollowStatus.FOLLOWING))
                .canOpenConnections,
        ).isTrue()
    }

    @Test
    fun `an account that blocked the viewer keeps plain numbers`() {
        assertThat(content(publicProfile, ProfileRelationship(isBlockedByTarget = true)).canOpenConnections).isFalse()
    }

    private companion object {
        val publicProfile = Profile(
            userId = "u1",
            username = "ada",
            displayName = "Ada Lovelace",
            bio = "",
            category = "personal",
            profession = "",
            website = "",
            location = "",
            badgeFlags = 0,
            isVerified = false,
            verificationLevel = "",
            statusText = "",
            statusEmoji = "",
            profileThemeColor = "",
            memberSinceBadge = false,
            counts = ProfileCounts(followers = 0, following = 0, friends = 0, posts = 0),
            createdAt = "2026-08-16T17:53:05Z",
        )

        val ownProfile = publicProfile.copy(
            userId = "me",
            personal = PersonalProfile(
                firstName = "Me",
                lastName = "",
                dateOfBirth = "",
                gender = "",
                timezone = "",
                introMediaUrl = "",
                ctaUrl = "",
                updatedAt = "",
            ),
        )
    }
}
