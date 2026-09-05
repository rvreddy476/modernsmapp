package com.us.android.feature.tube.ui.home

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/** The channels strip from the Following feed: one bubble per creator, newest first, the viewer left out. */
class TubeChannelsTest {

    @Test
    fun `one bubble per author, in the order their newest video appears`() {
        val bubbles = channelBubbles(
            listOf(video("p1", "ada"), video("p2", "bob"), video("p3", "ada"), video("p4", "cy")),
            ownId = "me",
        )

        assertThat(bubbles.map { it.userId }).containsExactly("ada", "bob", "cy").inOrder()
    }

    @Test
    fun `the viewer's own videos never make a bubble`() {
        val bubbles = channelBubbles(listOf(video("p1", "me"), video("p2", "ada")), ownId = "me")

        assertThat(bubbles.map { it.userId }).containsExactly("ada")
    }

    @Test
    fun `a channel's name and face win over the author's`() {
        val channel = FeedChannel(userId = "ada", name = "Ada's Engine", handle = "engine", avatarUrl = "https://a")
        val bubble = channelBubbles(listOf(video("p1", "ada").copy(channel = channel)), ownId = "me").single()

        assertThat(bubble.name).isEqualTo("Ada's Engine")
        assertThat(bubble.handle).isEqualTo("@engine")
        assertThat(bubble.avatarUrl).isEqualTo("https://a")
    }

    @Test
    fun `without a channel the author's name stands in, and the strip degrades rather than empties`() {
        val bubble = channelBubbles(listOf(video("p1", "ada")), ownId = "me").single()

        assertThat(bubble.name).isEqualTo("Ada")
        assertThat(bubble.handle).isEqualTo("@ada")
        assertThat(bubble.avatarUrl).isNull()
        assertThat(bubble.avatarMediaId).isEqualTo("av-ada")
    }

    @Test
    fun `a card credits the channel when there is one and the author otherwise`() {
        val plain = video("p1", "ada")
        val channelled = plain.copy(channel = FeedChannel(userId = "ada", name = "Engine", handle = "engine"))

        assertThat(plain.creatorName).isEqualTo("Ada")
        assertThat(plain.creatorHandle).isEqualTo("@ada")
        assertThat(channelled.creatorName).isEqualTo("Engine")
        assertThat(channelled.creatorHandle).isEqualTo("@engine")
    }

    private fun video(id: String, authorId: String) = FeedItem(
        id = id,
        authorId = authorId,
        author = FeedAuthor(
            id = authorId,
            displayName = authorId.replaceFirstChar(Char::uppercaseChar),
            username = authorId,
            avatarMediaId = "av-$authorId",
        ),
        text = "",
        title = "Video $id",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
    )
}
