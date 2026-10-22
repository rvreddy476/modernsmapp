package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import kotlin.random.Random

/**
 * Every share route the web serves (21 Oct 2026) parses back to the exact
 * item, with the query (`?ref=…&utm_source=share`), a trailing slash and a
 * fragment ignored; a bad id, an unknown route or another host opens
 * nothing. One good / bad / with-query case per route.
 */
class WebLinkRoutesTest {

    @Test
    fun `a post link opens the post`() {
        assertThat(WebLinks.targetOf("$ORIGIN/post/$ID")).isEqualTo(WebLinkTarget.Post(ID))
        assertThat(WebLinks.targetOf("$ORIGIN/post/$ID?ref=abc123def4&utm_source=share"))
            .isEqualTo(WebLinkTarget.Post(ID))
        assertThat(WebLinks.targetOf("$ORIGIN/post/not-a-uuid")).isNull()
    }

    @Test
    fun `a reel link opens Reels on that reel`() {
        assertThat(WebLinks.targetOf("$ORIGIN/reels/$ID")).isEqualTo(WebLinkTarget.Reel(ID))
        assertThat(WebLinks.targetOf("$ORIGIN/reels/$ID/?ref=abc123def4&utm_source=share#top"))
            .isEqualTo(WebLinkTarget.Reel(ID))
        assertThat(WebLinks.targetOf("$ORIGIN/reels/123")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/reels/")).isNull()
    }

    @Test
    fun `a watch link opens Tube's watch screen`() {
        assertThat(WebLinks.targetOf("$ORIGIN/posttube/watch/$ID")).isEqualTo(WebLinkTarget.Watch(ID))
        assertThat(WebLinks.targetOf("https://app.cleestudio.com/posttube/watch/$ID?utm_source=share"))
            .isEqualTo(WebLinkTarget.Watch(ID))
        assertThat(WebLinks.targetOf("$ORIGIN/posttube/watch/${ID}x")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/posttube/$ID")).isNull()
    }

    @Test
    fun `a profile link carries the username, without an at sign`() {
        assertThat(WebLinks.targetOf("$ORIGIN/u/ada")).isEqualTo(WebLinkTarget.Profile("ada"))
        assertThat(WebLinks.targetOf("$ORIGIN/u/%40ada.lovelace?ref=abc123def4&utm_source=share"))
            .isEqualTo(WebLinkTarget.Profile("ada.lovelace"))
        assertThat(WebLinks.targetOf("$ORIGIN/u/")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/u/ada/posts")).isNull()
        // A decoded slash or query can never reach a screen as a handle.
        assertThat(WebLinks.targetOf("$ORIGIN/u/ada%2F..")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/u/ada%3Fx")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/u/%ZZ")).isNull()
    }

    @Test
    fun `the profile link the app hands out parses back to the handle`() {
        assertThat(WebLinks.targetOf(WebLinks.profileUrl("@ada_1"))).isEqualTo(WebLinkTarget.Profile("ada_1"))
    }

    @Test
    fun `both channel routes carry the handle`() {
        assertThat(WebLinks.targetOf("$ORIGIN/posttube/channel/ada")).isEqualTo(WebLinkTarget.Channel("ada"))
        assertThat(WebLinks.targetOf("$ORIGIN/channel/ada")).isEqualTo(WebLinkTarget.Channel("ada"))
        assertThat(WebLinks.targetOf("$ORIGIN/channel/@ada?ref=abc123def4&utm_source=share"))
            .isEqualTo(WebLinkTarget.Channel("ada"))
        assertThat(WebLinks.targetOf("$ORIGIN/posttube/channel/")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/posttube/channel/ada/feed.xml")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/channel/a%20b")).isNull()
    }

    @Test
    fun `a sound link opens the sound page and is never read as a reel`() {
        assertThat(WebLinks.targetOf("$ORIGIN/reels/sound/snd_42")).isEqualTo(WebLinkTarget.Sound("snd_42"))
        assertThat(WebLinks.targetOf("$ORIGIN/reels/sound/$ID?utm_source=share")).isEqualTo(WebLinkTarget.Sound(ID))
        assertThat(WebLinks.targetOf("$ORIGIN/reels/sound/")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/reels/sound/a%2Fb")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/reels/sound")).isNull()
    }

    @Test
    fun `a hashtag link carries the tag without the hash`() {
        assertThat(WebLinks.targetOf("$ORIGIN/hashtag/sunrise")).isEqualTo(WebLinkTarget.Hashtag("sunrise"))
        assertThat(WebLinks.targetOf("$ORIGIN/hashtag/%23sunrise_2?ref=abc123def4&utm_source=share"))
            .isEqualTo(WebLinkTarget.Hashtag("sunrise_2"))
        // Any script, decoded from UTF-8.
        assertThat(WebLinks.targetOf("$ORIGIN/hashtag/%E0%A4%A8%E0%A4%AE%E0%A4%B8%E0%A5%8D%E0%A4%A4%E0%A5%87"))
            .isEqualTo(WebLinkTarget.Hashtag("नमस्ते"))
        assertThat(WebLinks.targetOf("$ORIGIN/hashtag/")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/hashtag/sun%20rise")).isNull()
        assertThat(WebLinks.targetOf("$ORIGIN/hashtag/" + "a".repeat(101))).isNull()
    }

    @Test
    fun `the shop, live and unknown routes, other hosts and plain http open nothing`() {
        listOf(
            "$ORIGIN/shop/products/$ID",
            "$ORIGIN/live/s1",
            "$ORIGIN/",
            "$ORIGIN/settings",
            "http://cleestudio.com/reels/$ID",
            "https://evil.example/reels/$ID",
            "https://cleestudio.com.evil.example/u/ada",
            "https://cleestudio.com:8443/hashtag/x",
            "https://atpost.app/chat/join/k7Qm",
            "",
            null,
        ).forEach { link -> assertThat(WebLinks.targetOf(link)).isNull() }
    }

    @Test
    fun `postIdOf still answers for the post route alone`() {
        assertThat(WebLinks.postIdOf("$ORIGIN/post/$ID")).isEqualTo(ID)
        assertThat(WebLinks.postIdOf("$ORIGIN/reels/$ID")).isNull()
    }

    @Test
    fun `a share ref is ten lower-case base36 characters, and only ours is kept`() {
        val ref = WebLinks.newShareRef(Random(7))
        assertThat(ref).matches("[a-z0-9]{10}")
        assertThat(WebLinks.isShareRef(ref)).isTrue()
        assertThat(WebLinks.isShareRef("ABC")).isFalse()
        assertThat(WebLinks.isShareRef("abc")).isFalse()
        assertThat(WebLinks.isShareRef(null)).isFalse()
    }

    @Test
    fun `withShareRef appends the web's tail, keeps a ref already there and keeps the fragment last`() {
        assertThat(WebLinks.withShareRef("$ORIGIN/post/$ID", "abc123def4"))
            .isEqualTo("$ORIGIN/post/$ID?ref=abc123def4&utm_source=share")
        assertThat(WebLinks.withShareRef("$ORIGIN/post/$ID?ref=zzzz&x=1#c", "abc123def4"))
            .isEqualTo("$ORIGIN/post/$ID?ref=zzzz&x=1&utm_source=share#c")
        assertThat(WebLinks.withShareRef("$ORIGIN/post/$ID?ref=BAD", "abc123def4"))
            .isEqualTo("$ORIGIN/post/$ID?ref=abc123def4&utm_source=share")
        assertThat(WebLinks.withShareRef("$ORIGIN/post/$ID", "no spaces")).isEqualTo("$ORIGIN/post/$ID")
        assertThat(WebLinks.withShareRef("", "abc123def4")).isEmpty()
        // The tail never changes what the link opens.
        assertThat(WebLinks.targetOf(WebLinks.withShareRef("$ORIGIN/posttube/watch/$ID")))
            .isEqualTo(WebLinkTarget.Watch(ID))
    }

    private companion object {
        const val ORIGIN = "https://cleestudio.com"
        const val ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }
}
