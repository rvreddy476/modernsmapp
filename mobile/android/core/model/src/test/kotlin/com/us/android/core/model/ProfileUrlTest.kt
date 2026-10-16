package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Share profile's address (T-21): the web client's public `/u/<username>`
 * page, built in [WebLinks] next to the post permalink.
 */
class ProfileUrlTest {

    @Test
    fun `a profile's address is the web client's public u route`() {
        assertThat(WebLinks.profileUrl("ada_lovelace")).isEqualTo("https://cleestudio.com/u/ada_lovelace")
    }

    @Test
    fun `a leading at sign and surrounding space are dropped`() {
        assertThat(WebLinks.profileUrl("  @ada.l  ")).isEqualTo("https://cleestudio.com/u/ada.l")
    }

    @Test
    fun `a blank handle has no address`() {
        assertThat(WebLinks.profileUrl("")).isNull()
        assertThat(WebLinks.profileUrl("   ")).isNull()
        assertThat(WebLinks.profileUrl("@")).isNull()
        assertThat(WebLinks.profileUrl(null)).isNull()
    }

    @Test
    fun `a handle can never add a path segment, a query or a fragment`() {
        assertThat(WebLinks.profileUrl("a/b?c#d")).isEqualTo("https://cleestudio.com/u/a%2Fb%3Fc%23d")
        assertThat(WebLinks.profileUrl("../admin")).isEqualTo("https://cleestudio.com/u/..%2Fadmin")
        assertThat(WebLinks.profileUrl("two words")).isEqualTo("https://cleestudio.com/u/two%20words")
    }

    @Test
    fun `unreserved characters stay as they are and others are UTF-8 percent-encoded`() {
        assertThat(WebLinks.profileUrl("A-z_0.9~")).isEqualTo("https://cleestudio.com/u/A-z_0.9~")
        assertThat(WebLinks.profileUrl("é")).isEqualTo("https://cleestudio.com/u/%C3%A9")
    }

    @Test
    fun `the profile address is on the same host as the post permalink`() {
        val profile = WebLinks.profileUrl("ada").orEmpty()
        val post = WebLinks.postPermalink("p")
        assertThat(profile.substringBefore("/u/")).isEqualTo(post.substringBefore("/post/"))
    }
}
