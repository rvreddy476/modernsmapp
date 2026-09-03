package com.us.android.core.profile

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.DirectMessageAudience
import org.junit.Test

class DirectMessageAudienceTest {

    @Test
    fun everyoneMessageRequestsShowsAllThreeRowsOn() {
        val audience = DirectMessageAudience.fromRaw("everyone_message_requests")
        assertThat(audience).isEqualTo(DirectMessageAudience(true, true, true))
    }

    @Test
    fun followersMessageRequestsShowsFollowersAndFriendsOn() {
        val audience = DirectMessageAudience.fromRaw("followers_message_requests")
        assertThat(audience).isEqualTo(DirectMessageAudience(false, true, true))
    }

    @Test
    fun connectionsOnlyShowsOnlyFriendsOn() {
        val audience = DirectMessageAudience.fromRaw("connections_only")
        assertThat(audience).isEqualTo(DirectMessageAudience(false, false, true))
    }

    @Test
    fun noOneShowsEverythingOff() {
        val audience = DirectMessageAudience.fromRaw("no_one")
        assertThat(audience).isEqualTo(DirectMessageAudience(false, false, false))
    }

    @Test
    fun retiredValuesFallBackToTheConnectionsOnlyRowState() {
        assertThat(DirectMessageAudience.fromRaw("connections_and_mutual_followers"))
            .isEqualTo(DirectMessageAudience(false, false, true))
        assertThat(DirectMessageAudience.fromRaw("friends_of_friends_requests"))
            .isEqualTo(DirectMessageAudience(false, false, true))
    }

    @Test
    fun turningEveryoneOnPicksEveryoneMessageRequestsRegardlessOfOtherRows() {
        val audience = DirectMessageAudience(everyoneRequests = true, followersRequests = false, friendsDirect = false)
        assertThat(audience.toRaw()).isEqualTo("everyone_message_requests")
    }

    @Test
    fun turningFollowersOnWithEveryoneOffPicksFollowersMessageRequests() {
        val audience = DirectMessageAudience(everyoneRequests = false, followersRequests = true, friendsDirect = false)
        assertThat(audience.toRaw()).isEqualTo("followers_message_requests")
    }

    @Test
    fun onlyFriendsOnPicksConnectionsOnly() {
        val audience = DirectMessageAudience(everyoneRequests = false, followersRequests = false, friendsDirect = true)
        assertThat(audience.toRaw()).isEqualTo("connections_only")
    }

    @Test
    fun everythingOffPicksNoOne() {
        val audience = DirectMessageAudience(everyoneRequests = false, followersRequests = false, friendsDirect = false)
        assertThat(audience.toRaw()).isEqualTo("no_one")
    }

    @Test
    fun roundTripsThroughTheFourCanonicalValues() {
        listOf("everyone_message_requests", "followers_message_requests", "connections_only", "no_one")
            .forEach { raw ->
                assertThat(DirectMessageAudience.fromRaw(raw).toRaw()).isEqualTo(raw)
            }
    }
}
