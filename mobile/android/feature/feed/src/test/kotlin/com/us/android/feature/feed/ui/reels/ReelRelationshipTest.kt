package com.us.android.feature.feed.ui.reels

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.FollowStatus
import org.junit.Test

/**
 * Which pill a reel's overlay draws (2026-09-12): Subscribe only when the
 * row carries the author's channel, because subscribing to an author with
 * no channel is a 404 the viewer would see as a button that does nothing.
 */
class ReelRelationshipTest {

    private fun reel(channel: FeedChannel? = null) = FeedItem(
        id = "p",
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        channel = channel,
    )

    @Test
    fun `a reel whose author has a channel offers Subscribe, keyed by the channel`() {
        val relationship = reelRelationship(reel(FeedChannel(userId = "chan", name = "Ada's channel", handle = "ada")))

        assertThat(relationship).isEqualTo(ReelRelationship.Subscribe("chan"))
        assertThat(relationship.label).isEqualTo("Subscribe")
    }

    @Test
    fun `a reel without a channel stays Follow, keyed by the author`() {
        val relationship = reelRelationship(reel())

        assertThat(relationship).isEqualTo(ReelRelationship.Follow("a"))
        assertThat(relationship.label).isEqualTo("Follow")
    }

    /** A channel stub with no id is no channel: the server could not resolve a subscribe for it. */
    @Test
    fun `a channel with a blank id does not turn the pill into Subscribe`() {
        assertThat(reelRelationship(reel(FeedChannel(userId = "", name = "x", handle = "x"))))
            .isEqualTo(ReelRelationship.Follow("a"))
    }

    @Test
    fun `Follow is offered from the follow graph and Subscribe from the subscription graph`() {
        val follow = ReelRelationship.Follow("a")
        val subscribe = ReelRelationship.Subscribe("chan")
        val notFollowing = mapOf("a" to FollowStatus.NONE)
        val notSubscribed = mapOf("chan" to ChannelSubscription.NOT_SUBSCRIBED)

        assertThat(offersReelRelationship(follow, "me", notFollowing, emptyMap())).isTrue()
        assertThat(offersReelRelationship(follow, "me", emptyMap(), notSubscribed)).isFalse()
        assertThat(offersReelRelationship(subscribe, "me", notFollowing, emptyMap())).isFalse()
        assertThat(offersReelRelationship(subscribe, "me", emptyMap(), notSubscribed)).isTrue()
    }

    @Test
    fun `neither pill is offered on the viewer's own reel or once the edge is made`() {
        val subscribed = mapOf("chan" to ChannelSubscription(subscribed = true))

        assertThat(offersReelRelationship(ReelRelationship.Subscribe("chan"), "chan", emptyMap(), emptyMap())).isFalse()
        assertThat(offersReelRelationship(ReelRelationship.Subscribe("chan"), "me", emptyMap(), subscribed)).isFalse()
        assertThat(offersReelRelationship(ReelRelationship.Follow("a"), "a", emptyMap(), emptyMap())).isFalse()
        assertThat(offersReelRelationship(ReelRelationship.Follow("a"), "me", mapOf("a" to FollowStatus.FOLLOWING), emptyMap()))
            .isFalse()
    }
}
