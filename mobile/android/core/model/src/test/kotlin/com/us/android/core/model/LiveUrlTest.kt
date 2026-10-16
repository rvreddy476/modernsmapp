package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Share in the live room (T-30): the web client's `/live/<streamId>` page,
 * built in [WebLinks] next to the post permalink and the profile address.
 */
class LiveUrlTest {

    @Test
    fun `a stream's address is the web client's live route`() {
        assertThat(WebLinks.liveUrl("a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"))
            .isEqualTo("https://cleestudio.com/live/a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1")
    }

    @Test
    fun `a blank id has no address`() {
        assertThat(WebLinks.liveUrl("")).isNull()
        assertThat(WebLinks.liveUrl("  ")).isNull()
        assertThat(WebLinks.liveUrl(null)).isNull()
    }

    @Test
    fun `an id can never add a path segment, a query or a fragment`() {
        assertThat(WebLinks.liveUrl("a/b?c#d")).isEqualTo("https://cleestudio.com/live/a%2Fb%3Fc%23d")
        assertThat(WebLinks.liveUrl(" s1 ")).isEqualTo("https://cleestudio.com/live/s1")
    }

    @Test
    fun `the live address is on the same host as the post permalink`() {
        val live = WebLinks.liveUrl("s1").orEmpty()
        val post = WebLinks.postPermalink("p")
        assertThat(live.substringBefore("/live/")).isEqualTo(post.substringBefore("/post/"))
    }
}
