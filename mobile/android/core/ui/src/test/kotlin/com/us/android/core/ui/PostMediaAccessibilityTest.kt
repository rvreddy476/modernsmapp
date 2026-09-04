package com.us.android.core.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.createComposeRule
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * What a screen reader actually says about a post's image — Slice C, C-CLB-3.
 *
 * ## WHY THE SEMANTICS TREE AND NOT THE SOURCE
 *
 * The defect this closes was one line: `AsyncImage(contentDescription = null)`.
 * Everything upstream of it was correct — the composer demanded a description,
 * the server stored it — and a reader of the source would have seen a
 * well-formed accessibility feature that announced nothing at all.
 *
 * Reading source proves a parameter is passed. Only the semantics tree proves
 * what would be spoken, which is the claim that actually matters, so that is
 * what this asserts.
 *
 * Robolectric on the unit source set, deliberately: an instrumented test needs
 * a device attached, so in practice it would run rarely and guard nothing
 * between runs. `:core:database` and `:feature:chat` already prove Robolectric
 * works in this build.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class PostMediaAccessibilityTest {

    @get:Rule
    val composeRule = createComposeRule()

    /** Every content description currently in the tree. */
    private fun descriptions(): List<String> =
        composeRule.onAllNodes(
            SemanticsMatcher.keyIsDefined(SemanticsProperties.ContentDescription),
        ).fetchSemanticsNodes()
            .flatMap { it.config[SemanticsProperties.ContentDescription] }

    private fun renderMedia(contentDescription: String?) {
        composeRule.setContent {
            UsTheme {
                PostMedia(
                    // A non-null URL so the AsyncImage node is composed. Coil
                    // will not resolve it under Robolectric and does not need
                    // to: the semantics node exists either way, and it is the
                    // node — not the bitmap — under test.
                    url = "https://example.invalid/photo.jpg",
                    postType = "image",
                    count = 1,
                    contentDescription = contentDescription,
                )
            }
        }
        composeRule.waitForIdle()
    }

    /**
     * A described photo is announced, in the author's own words.
     *
     * This is the assertion whose absence let the write-only description ship.
     */
    @Test
    fun `a described image announces the author's description`() {
        renderMedia("a cat asleep on a keyboard")

        assertThat(descriptions()).contains("a cat asleep on a keyboard")
    }

    /**
     * A decorative photo stays silent.
     *
     * The author said it carries no information; announcing anything — even a
     * generic "image" — would be noise they explicitly declined to add.
     */
    @Test
    fun `a decorative image is announced by nothing at all`() {
        renderMedia(null)

        assertThat(descriptions()).isEmpty()
    }

    /**
     * The description survives the whole CARD, not just the leaf composable.
     *
     * The original defect was one dropped value. A test that only ever called
     * `PostMedia` directly would stay green if `PostCardState`'s description
     * were lost on the way down — the same shape of defect, one layer up. So
     * this drives the real `PostCard` with a real state object.
     */
    @Test
    fun `the card carries the description down to the image`() {
        composeRule.setContent {
            UsTheme {
                PostCard(
                    state = cardState(ANNOUNCED),
                    onClick = {},
                    onAuthorClick = {},
                    onReact = {},
                    onComment = {},
                    onRepost = {},
                    onBookmark = {},
                    onShare = {},
                )
            }
        }
        composeRule.waitForIdle()

        assertThat(descriptions()).contains(ANNOUNCED)
    }

    /** A decorative photo stays silent through the card too. */
    @Test
    fun `the card announces nothing for a decorative image`() {
        composeRule.setContent {
            UsTheme {
                PostCard(
                    state = cardState(null),
                    onClick = {},
                    onAuthorClick = {},
                    onReact = {},
                    onComment = {},
                    onRepost = {},
                    onBookmark = {},
                    onShare = {},
                )
            }
        }
        composeRule.waitForIdle()

        assertThat(descriptions()).doesNotContain(ANNOUNCED)
    }

    private fun cardState(description: String?) = PostCardState(
        postId = "p1",
        authorId = "a1",
        authorName = "Author",
        text = "hello",
        timestamp = "now",
        postType = "image",
        mediaCount = 1,
        mediaUrl = "https://example.invalid/photo.jpg",
        mediaContentDescription = description,
        actions = PostActionState(
            likeCount = 0,
            commentCount = 0,
            repostCount = 0,
            hasReacted = false,
            isBookmarked = false,
        ),
    )

    private companion object {
        const val ANNOUNCED = "a red bicycle against a wall"
    }
}
