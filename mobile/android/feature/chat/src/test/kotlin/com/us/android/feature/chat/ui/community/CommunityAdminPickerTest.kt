package com.us.android.feature.chat.ui.community

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CommunityAdmin
import com.us.android.core.chat.data.CommunityAdminFailure
import com.us.android.core.chat.data.PersonHit
import org.junit.Test

/**
 * The add-admin picker offers only people channel-service will accept
 * (members, not banned, not admins already), and every refusal has words.
 */
class CommunityAdminPickerTest {

    private fun hit(id: String) = PersonHit(userId = id, username = id, displayName = "", avatarUrl = null)

    @Test
    fun `the picker offers only members who can be made admins`() {
        val hits = listOf(hit("member"), hit("stranger"), hit("banned"), hit("admin"))
        val candidates = setOf("member", "admin")
        val admins = listOf(CommunityAdmin("admin", "Admin"))

        assertThat(adminPickerHits(hits, candidates, admins).map { it.userId }).containsExactly("member")
    }

    @Test
    fun `nobody is offered while the member roster is unknown`() {
        assertThat(adminPickerHits(listOf(hit("member")), candidates = null, admins = emptyList())).isEmpty()
    }

    @Test
    fun `the picker says why it shows fewer people than it found`() {
        assertThat(adminPickerHint(found = 3, offered = 1, rosterKnown = true))
            .isEqualTo("Only members of this community can be made admins.")
        assertThat(adminPickerHint(found = 2, offered = 0, rosterKnown = false))
            .isEqualTo("Couldn't check who's a member. Try the search again.")
        assertThat(adminPickerHint(found = 2, offered = 2, rosterKnown = true)).isNull()
        assertThat(adminPickerHint(found = 0, offered = 0, rosterKnown = false)).isNull()
    }

    @Test
    fun `every refused admin change says what happened`() {
        assertThat(communityAdminFailureText(CommunityAdminFailure.NotAMember))
            .isEqualTo("Only members can be admins. They need to join the community first.")
        assertThat(communityAdminFailureText(CommunityAdminFailure.Banned))
            .isEqualTo("They're banned from this community. Unban them before making them an admin.")
        assertThat(communityAdminFailureText(CommunityAdminFailure.NotAllowed))
            .isEqualTo("Only the community's owner can change its admins.")
        assertThat(communityAdminFailureText(CommunityAdminFailure.Unavailable)).isEqualTo(COMMUNITY_UNAVAILABLE_TEXT)
        assertThat(communityAdminFailureText(CommunityAdminFailure.Other))
            .isEqualTo("That didn't go through. Try again.")
    }
}
