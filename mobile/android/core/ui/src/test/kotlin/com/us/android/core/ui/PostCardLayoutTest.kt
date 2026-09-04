package com.us.android.core.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Instagram card's layout rules (founder, 2026-09-04), asserted from the
 * laid-out tree rather than the source.
 *
 * Two rules carry the whole design. Every media post — one photo, a
 * carousel, a reel poster — sits in the SAME 4:5 frame, so the feed scrolls
 * at one rhythm; a text-only post has no frame at all, and its height
 * follows its text. And the header offers "Follow" only when the host says
 * so: the card cannot decide on its own, so what it must guarantee is that a
 * null callback renders no control.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w411dp-h900dp")
class PostCardLayoutTest {

    @get:Rule
    val composeRule = createComposeRule()

    private fun render(state: PostCardState, onFollow: (() -> Unit)? = null, onMore: (() -> Unit)? = null) {
        composeRule.setContent {
            UsTheme {
                // A scrolling host, as the feed's LazyColumn is: the card gets
                // unbounded height, so the frame is sized by its width alone.
                // Robolectric's default screen is 470dp tall, and a bounded
                // host would clamp the frame to whatever height is left.
                Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                    PostCard(
                        state = state,
                        onClick = {},
                        onAuthorClick = {},
                        onReact = {},
                        onComment = {},
                        onRepost = {},
                        onBookmark = {},
                        onShare = {},
                        onFollow = onFollow,
                        onMore = onMore,
                    )
                }
            }
        }
        composeRule.waitForIdle()
    }

    /**
     * width / height of every media frame in the tree, from the laid-out
     * size — `size`, not `boundsInRoot`, which is CLIPPED to the root window
     * and would report a frame taller than the screen as shorter than it is.
     */
    private fun frameAspects(): List<Float> =
        composeRule.onAllNodesWithTag("post_media_frame").fetchSemanticsNodes().map { node ->
            node.size.width.toFloat() / node.size.height.toFloat()
        }

    // ── The 4:5 frame ──────────────────────────────────────────────────

    @Test
    fun `a photo post's media frame is 4 by 5`() {
        render(state(postType = "image", mediaCount = 1))

        assertThat(frameAspects()).hasSize(1)
        assertThat(frameAspects().single()).isWithin(TOLERANCE).of(MEDIA_FRAME_ASPECT)
    }

    @Test
    fun `a reel's poster frame is the same 4 by 5`() {
        render(state(postType = "video", mediaCount = 1))

        assertThat(frameAspects().single()).isWithin(TOLERANCE).of(MEDIA_FRAME_ASPECT)
        composeRule.onNodeWithText("Reel").assertIsDisplayed()
    }

    /** Every page of a carousel is the frame; a landscape page is cropped, not letterboxed. */
    @Test
    fun `every carousel page is the same 4 by 5 frame`() {
        val pages = listOf(16f / 9f, 1f, 9f / 16f).mapIndexed { i, aspect ->
            PostCardMediaPage(mediaId = "m$i", url = "https://example.invalid/$i.jpg", aspectRatio = aspect)
        }
        render(state(postType = "image", mediaCount = pages.size, mediaPages = pages))

        val aspects = frameAspects()
        assertThat(aspects).isNotEmpty()
        aspects.forEach { assertThat(it).isWithin(TOLERANCE).of(MEDIA_FRAME_ASPECT) }
    }

    @Test
    fun `a text-only post has no media frame and shows its text as the body`() {
        render(state(postType = "text", mediaCount = 0, text = "Just words."))

        composeRule.onAllNodesWithTag("post_media_frame").assertCountEquals(0)
        composeRule.onNodeWithTag("post_text_body").assertIsDisplayed()
        composeRule.onNodeWithText("Just words.").assertIsDisplayed()
    }

    // ── The header ─────────────────────────────────────────────────────

    @Test
    fun `follow is rendered only when the host offers it`() {
        render(state(postType = "image", mediaCount = 1), onFollow = {})
        composeRule.onNodeWithTag("post_follow").assertIsDisplayed()
    }

    @Test
    fun `no follow callback means no follow control`() {
        render(state(postType = "image", mediaCount = 1), onFollow = null)
        composeRule.onAllNodesWithTag("post_follow").assertCountEquals(0)
    }

    @Test
    fun `the more glyph is rendered only when the host passes onMore`() {
        render(state(postType = "image", mediaCount = 1), onMore = {})
        composeRule.onNodeWithTag("post_more").assertIsDisplayed()
    }

    @Test
    fun `no onMore means no more glyph`() {
        render(state(postType = "image", mediaCount = 1), onMore = null)
        composeRule.onAllNodesWithTag("post_more").assertCountEquals(0)
    }

    /** Instagram leads with the handle; an account without one still needs a name. */
    @Test
    fun `the header prints the username, falling back to the display name`() {
        assertThat(state(authorHandle = "@ada").username).isEqualTo("ada")
        assertThat(state(authorHandle = null).username).isEqualTo("Ada Lovelace")
        assertThat(state(authorHandle = "@").username).isEqualTo("Ada Lovelace")
    }

    private fun state(
        postType: String = "image",
        mediaCount: Int = 1,
        text: String = "caption",
        mediaPages: List<PostCardMediaPage> = emptyList(),
        authorHandle: String? = "@ada",
    ) = PostCardState(
        postId = "p1",
        authorId = "a1",
        authorName = "Ada Lovelace",
        authorHandle = authorHandle,
        text = text,
        timestamp = "2h",
        postType = postType,
        mediaCount = mediaCount,
        mediaUrl = "https://example.invalid/photo.jpg".takeIf { mediaCount > 0 },
        mediaPages = mediaPages,
        actions = PostActionState(
            likeCount = 3,
            commentCount = 1,
            repostCount = 0,
            hasReacted = false,
            isBookmarked = false,
        ),
    )

    private companion object {
        const val TOLERANCE = 0.02f
    }
}
