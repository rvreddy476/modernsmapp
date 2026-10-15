package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The public post permalink and its App Link parser (T-07): what "Copy link"
 * and Share hand out must be exactly what the manifest routes back in.
 */
class WebLinksTest {

    @Test
    fun `a post's permalink is the web client's public post route`() {
        assertThat(WebLinks.postPermalink(POST_ID)).isEqualTo("https://cleestudio.com/post/$POST_ID")
    }

    @Test
    fun `the permalink is never the old p route, which 404s`() {
        assertThat(WebLinks.postPermalink(POST_ID)).doesNotContain("/p/")
    }

    @Test
    fun `the parser reads back the id the builder wrote`() {
        assertThat(WebLinks.postIdOf(WebLinks.postPermalink(POST_ID))).isEqualTo(POST_ID)
    }

    @Test
    fun `the app subdomain is accepted and the invite host is not`() {
        assertThat(WebLinks.postIdOf("https://app.cleestudio.com/post/$POST_ID")).isEqualTo(POST_ID)
        assertThat(WebLinks.postIdOf("https://atpost.app/post/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://evil.cleestudio.com/post/$POST_ID")).isNull()
    }

    @Test
    fun `a trailing slash, query and fragment are ignored`() {
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/$POST_ID/")).isEqualTo(POST_ID)
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/$POST_ID?utm=x#c")).isEqualTo(POST_ID)
        assertThat(WebLinks.postIdOf("  https://CLEESTUDIO.com/post/$POST_ID  ")).isEqualTo(POST_ID)
    }

    @Test
    fun `an id that is not a UUID is refused`() {
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/123")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/not-a-uuid")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/..%2F..")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/${POST_ID}x")).isNull()
    }

    @Test
    fun `another path, host or scheme is refused`() {
        assertThat(WebLinks.postIdOf("https://cleestudio.com/p/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com/post/$POST_ID/comments")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com/reels/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://atpost.app/chat/join/k7Qm")).isNull()
        assertThat(WebLinks.postIdOf("http://cleestudio.com/post/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://evil.example/post/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com.evil.example/post/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://cleestudio.com:8443/post/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("https://user@cleestudio.com/post/$POST_ID")).isNull()
        assertThat(WebLinks.postIdOf("")).isNull()
        assertThat(WebLinks.postIdOf(null)).isNull()
    }

    private companion object {
        const val POST_ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }
}
