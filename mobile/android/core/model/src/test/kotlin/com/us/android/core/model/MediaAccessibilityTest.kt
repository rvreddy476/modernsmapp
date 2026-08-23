package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The one rule that decides whether an image is announced — Slice C, C-CLB-3.
 *
 * ## WHY THIS RULE IS ON THE MODEL AND TESTED HERE
 *
 * The composer refuses to publish an image until its author either describes it
 * or explicitly marks it decorative. That requirement was write-only: the
 * decision reached the database and stopped there, so every reader saw
 * `contentDescription = null` and the work was thrown away before reaching the
 * person it was for.
 *
 * Carrying it is only half the fix. Two renderers consume it — the feed card
 * and the post detail screen — and if each decided independently what an empty
 * description meant, they would eventually disagree about the same image. The
 * rule lives on the model, both call it, and this pins it.
 *
 * The distinction that matters is between the two silences:
 *
 *  - DECORATIVE is a statement. Someone looked at the photo and said it carries
 *    no information, so announcing it would be noise.
 *  - UNDECIDED is an absence. Nobody ever said. It renders the same way, but it
 *    is not the same claim, and collapsing them is what makes an accessibility
 *    gap invisible — the screen reader is silent either way and only one of
 *    those silences was chosen by a person.
 */
class MediaAccessibilityTest {

    // ── PostMediaRef ────────────────────────────────────────────────────

    @Test
    fun `a described image is announced with the author's own words`() {
        val media = PostMediaRef(
            mediaId = "m1",
            kind = "image",
            altText = "a cat asleep on a keyboard",
        )

        assertThat(media.contentDescription).isEqualTo("a cat asleep on a keyboard")
    }

    @Test
    fun `a decorative image is deliberately not announced`() {
        val media = PostMediaRef(mediaId = "m1", kind = "image", altDecorative = true)

        assertThat(media.contentDescription).isNull()
    }

    /**
     * Decorative WINS over a description that should not exist.
     *
     * The database has a CHECK constraint making these mutually exclusive, so
     * this state should be unreachable. If it ever occurs, announcing text the
     * author marked as carrying no information is the worse of the two
     * failures — so the flag decides.
     */
    @Test
    fun `decorative wins if a stray description somehow survives`() {
        val media = PostMediaRef(
            mediaId = "m1",
            kind = "image",
            altText = "left over",
            altDecorative = true,
        )

        assertThat(media.contentDescription).isNull()
    }

    /** Legacy rows from before the requirement existed. */
    @Test
    fun `an undecided image is not announced either`() {
        assertThat(PostMediaRef(mediaId = "m1", kind = "image").contentDescription).isNull()
    }

    /**
     * Whitespace is not a description.
     *
     * A field holding " " passes a naive isEmpty check and then announces
     * nothing audible, which reads to a screen-reader user as a broken label
     * rather than an absent one.
     */
    @Test
    fun `a blank description is treated as no description`() {
        val media = PostMediaRef(mediaId = "m1", kind = "image", altText = "   ")

        assertThat(media.contentDescription).isNull()
    }

    // ── FeedMedia: the same rule, because the feed is where images are seen ──

    @Test
    fun `feed media announces a described image`() {
        val media = FeedMedia(mediaId = "m1", kind = "image", altText = "a red bicycle")

        assertThat(media.contentDescription).isEqualTo("a red bicycle")
    }

    @Test
    fun `feed media leaves a decorative image silent`() {
        val media = FeedMedia(mediaId = "m1", kind = "image", altDecorative = true)

        assertThat(media.contentDescription).isNull()
    }

    /**
     * The two models must never disagree about the same image.
     *
     * The feed card and the detail screen render the same post. If the rules
     * drifted apart, an image would be announced in one place and silent in the
     * other, and nothing else in the suite would notice.
     */
    @Test
    fun `both models resolve identical inputs identically`() {
        val cases = listOf(
            "a described photo" to false,
            "" to true,
            "" to false,
            "   " to false,
            "stray text" to true,
        )

        for ((alt, decorative) in cases) {
            val post = PostMediaRef("m", "image", alt, decorative).contentDescription
            val feed = FeedMedia(mediaId = "m", kind = "image", altText = alt, altDecorative = decorative)
                .contentDescription

            assertThat(feed).isEqualTo(post)
        }
    }
}
