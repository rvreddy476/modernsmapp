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
        assertThat(body).isEqualTo("sunrise over the ghats\n\n— Ada\n\nhttps://cleestudio.com/post/$POST_ID")
    }

    @Test
    fun `the link in the share is the one Copy link puts on the clipboard`() {
        assertThat(postShareText(POST_ID, "words", null)).endsWith(postShareLink(POST_ID))
    }

    @Test
    fun `a post with no words and no author still shares its link`() {
        assertThat(postShareText(POST_ID, "  ", null)).isEqualTo("https://cleestudio.com/post/$POST_ID")
        assertThat(postShareText(POST_ID, "", "Ada")).isEqualTo("Ada\n\nhttps://cleestudio.com/post/$POST_ID")
    }

    @Test
    fun `an unknown author is never attributed`() {
        assertThat(postShareText(POST_ID, "words", null)).isEqualTo("words\n\nhttps://cleestudio.com/post/$POST_ID")
        assertThat(postShareText(POST_ID, "words", " ")).doesNotContain("—")
    }

    private companion object {
        const val POST_ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }
}
