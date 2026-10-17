package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.testing.MainDispatcherRule
import org.junit.Rule
import org.junit.Test

/**
 * A shared post in a thread (2026-10-17): the card is the post itself, read
 * once per post, drawn from its still, author and caption; a post the viewer
 * cannot read is a card that says so.
 */
class PostCardsTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private fun item(
        id: String = "p1",
        type: String = "flick",
        title: String = "",
        text: String = "  sunset run  ",
        media: List<FeedMedia> = emptyList(),
    ) = FeedItem(
        id = id,
        authorId = "u1",
        author = FeedAuthor(id = "u1", displayName = "Ana", username = "ana"),
        text = text,
        title = title,
        visibility = "public",
        feedContentType = type,
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = media,
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    @Test
    fun `a card carries the author, the caption and the still`() {
        val card = postCardOf(item(), thumbnailUrl = "https://cdn.test/t.jpg")

        assertThat(card).isEqualTo(
            PostCard(
                postId = "p1",
                authorName = "Ana",
                caption = "sunset run",
                thumbnailUrl = "https://cdn.test/t.jpg",
                contentType = "flick",
            ),
        )
        assertThat(card.isVideo).isTrue()
    }

    @Test
    fun `a long video's title is its caption, and a photo post is not a video`() {
        assertThat(postCardOf(item(type = "long_video", title = "Trip vlog"), null).caption).isEqualTo("Trip vlog")
        assertThat(postCardOf(item(type = "long_video"), null).isVideo).isTrue()
        assertThat(postCardOf(item(type = "post"), null).isVideo).isFalse()
        assertThat(postCardOf(item(), thumbnailUrl = " ").thumbnailUrl).isNull()
    }

    private val urls = MediaUrlResolver(
        ApiConfig(
            baseUrl = "https://api.test",
            wsBaseUrl = "wss://api.test",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    @Test
    fun `each post is read once, however often it was shared`() {
        val reads = mutableListOf<String>()
        val vm = PostCardsViewModel(PostCardSource { id -> reads += id; AppResult.Success(item(id = id)) }, urls)

        vm.ensure("p1")
        vm.ensure("p1")
        vm.ensure("p2")

        assertThat(reads).containsExactly("p1", "p2").inOrder()
        val card = (vm.cards.value["p1"] as PostCardState.Ready).card
        assertThat(card.authorName).isEqualTo("Ana")
    }

    @Test
    fun `a photo post's card uses its first image`() {
        val photo = FeedMedia(mediaId = "m1", kind = "image", variants = mapOf("720p" to "https://cdn.test/720.jpg"))
        val vm = PostCardsViewModel(PostCardSource { AppResult.Success(item(type = "post", media = listOf(photo))) }, urls)

        vm.ensure("p1")

        assertThat((vm.cards.value["p1"] as PostCardState.Ready).card.thumbnailUrl).isEqualTo("https://cdn.test/720.jpg")
    }

    @Test
    fun `a post that cannot be read is unavailable, and a blank id is never read`() {
        val vm = PostCardsViewModel(PostCardSource { AppResult.Failure(AppError.NotFound()) }, urls)

        vm.ensure("gone")
        vm.ensure("")

        assertThat(vm.cards.value).containsExactly("gone", PostCardState.Unavailable)
    }
}
