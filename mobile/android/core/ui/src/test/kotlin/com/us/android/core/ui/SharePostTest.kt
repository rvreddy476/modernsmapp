package com.us.android.core.ui

import android.content.Intent
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Share carries the post's public permalink (T-07): before 2026-10-14 it
 * carried only the words, so a recipient had no way to reach the post.
 * Since 2026-10-22 the link ends in the web's share tail
 * (`?ref=<id>&utm_source=share`), so every assertion matches up to the ref.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SharePostTest {

    @Test
    fun `the share intent's text carries the post's permalink`() {
        val intent = postShareIntent(POST_ID, "sunrise over the ghats", "Ada")

        assertThat(intent.action).isEqualTo(Intent.ACTION_SEND)
        assertThat(intent.type).isEqualTo("text/plain")
        val body = intent.getStringExtra(Intent.EXTRA_TEXT)
        assertThat(body).contains("https://cleestudio.com/post/$POST_ID")
        assertThat(body).matches("sunrise over the ghats\n\n— Ada\n\nhttps://cleestudio.com/post/$POST_ID$SHARE_TAIL")
    }

    @Test
    fun `the link in the share is the one Copy link puts on the clipboard`() {
        // Each call mints its own ref, so the two agree up to the ref.
        assertThat(postShareText(POST_ID, "words", null).substringBefore("?ref="))
            .endsWith(postShareLink(POST_ID).substringBefore("?ref="))
        assertThat(postShareLink(POST_ID)).matches("https://cleestudio.com/post/$POST_ID$SHARE_TAIL")
    }

    @Test
    fun `a post with no words and no author still shares its link`() {
        assertThat(postShareText(POST_ID, "  ", null)).matches("https://cleestudio.com/post/$POST_ID$SHARE_TAIL")
        assertThat(postShareText(POST_ID, "", "Ada")).matches("Ada\n\nhttps://cleestudio.com/post/$POST_ID$SHARE_TAIL")
    }

    @Test
    fun `an unknown author is never attributed`() {
        assertThat(postShareText(POST_ID, "words", null)).matches("words\n\nhttps://cleestudio.com/post/$POST_ID$SHARE_TAIL")
        assertThat(postShareText(POST_ID, "words", " ")).doesNotContain("—")
    }

    private companion object {
        const val POST_ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"

        /** The web's share tail: `?ref=<10 chars base36>&utm_source=share`. */
        const val SHARE_TAIL = "\\?ref=[a-z0-9]{10}&utm_source=share"
    }
}
