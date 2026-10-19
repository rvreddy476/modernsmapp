package com.us.android.feature.post.ui

import androidx.compose.ui.layout.ContentScale
import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedDisclosure
import com.us.android.core.model.Post
import com.us.android.core.model.PostCounts
import com.us.android.core.model.PostMediaRef
import com.us.android.core.model.PostViewerState
import org.junit.Test

/**
 * The full-screen post viewer's rules (2026-10-19): what a page draws, what
 * its caption says — the title the old card page never showed — and how a
 * video fills the stage.
 */
class PostStageRulesTest {

    private fun post(
        media: List<PostMediaRef> = emptyList(),
        title: String = "",
        text: String = "",
        hashtags: List<String> = emptyList(),
        disclosure: FeedDisclosure = FeedDisclosure(),
    ) = Post(
        id = "p1",
        authorId = "a1",
        text = text,
        visibility = "public",
        title = title,
        postType = if (media.isEmpty()) "text" else "image",
        createdAt = "",
        counts = PostCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = PostViewerState(isBookmarked = false),
        allowsComments = true,
        allowsReactions = true,
        isRepostable = true,
        isPinned = false,
        media = media,
        hashtags = hashtags,
        disclosure = disclosure,
    )

    private val photo = PostMediaRef(mediaId = "m1", kind = "image")
    private val video = PostMediaRef(mediaId = "v1", kind = "video")

    @Test
    fun `a page draws photos, a video, or the words`() {
        assertThat(postStageKind(post(media = listOf(photo, photo)))).isEqualTo(PostStageKind.PHOTO)
        assertThat(postStageKind(post(media = listOf(video)))).isEqualTo(PostStageKind.VIDEO)
        assertThat(postStageKind(post())).isEqualTo(PostStageKind.TEXT)
    }

    @Test
    fun `a photo post's caption carries its title, text, tags and declared labels`() {
        val caption = postStageCaption(
            post(
                media = listOf(photo),
                title = "Draft carousel",
                text = "Sunday market",
                hashtags = listOf("food"),
                disclosure = FeedDisclosure(isBranded = true, aiGenerated = true),
            ),
            handle = "@call_a",
        )

        assertThat(caption.title).isEqualTo("Draft carousel")
        assertThat(caption.text).isEqualTo("Sunday market")
        assertThat(caption.hashtags).containsExactly("food")
        assertThat(caption.labels).containsExactly("Brand", "AI-generated").inOrder()
        assertThat(caption.handle).isEqualTo("@call_a")
    }

    @Test
    fun `a text post's words are on the page, so the caption does not repeat them`() {
        val caption = postStageCaption(post(title = "Note", text = "Hello"), handle = "@a")

        assertThat(caption.text).isEmpty()
        assertThat(caption.title).isNull()
    }

    @Test
    fun `a video close to the screen's shape is cropped and anything else is fitted`() {
        assertThat(postVideoScale(9f / 19.5f)).isEqualTo(ContentScale.Crop)
        assertThat(postVideoScale(16f / 9f)).isEqualTo(ContentScale.Fit)
        assertThat(postVideoScale(0f)).isEqualTo(ContentScale.Fit)
    }
}
