package com.us.android.push

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Every web share route (21 Oct 2026) becomes one pending destination, typed
 * so the nav host can route it — one test per destination. The query is
 * ignored, a bad id routes nowhere, and the profile and channel types carry
 * a HANDLE the nav host resolves to a user id before it navigates.
 */
class PushDestinationsWebLinkTest {

    @Test
    fun `a reel link is a reel destination carrying the post id`() {
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/reels/$ID?ref=abc123def4&utm_source=share"))
            .isEqualTo(PushDestination(PushDestinations.TYPE_REEL_LINK, ID, "$ORIGIN/reels/$ID?ref=abc123def4&utm_source=share"))
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/reels/nope")).isNull()
    }

    @Test
    fun `a watch link is a watch destination carrying the post id`() {
        val link = "$ORIGIN/posttube/watch/$ID"
        assertThat(PushDestinations.linkDestinationOf(link))
            .isEqualTo(PushDestination(PushDestinations.TYPE_WATCH_LINK, ID, link))
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/posttube/watch/")).isNull()
    }

    @Test
    fun `a profile link is a profile destination carrying the username`() {
        val link = "$ORIGIN/u/ada?utm_source=share"
        val destination = PushDestinations.linkDestinationOf(link)
        assertThat(destination).isEqualTo(PushDestination(PushDestinations.TYPE_PROFILE_LINK, "ada", link))
        assertThat(destination!!.type).isIn(PushDestinations.HANDLE_LINK_TYPES)
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/u/a%2Fb")).isNull()
    }

    @Test
    fun `both channel links are a channel destination carrying the handle`() {
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/posttube/channel/ada")?.type)
            .isEqualTo(PushDestinations.TYPE_CHANNEL_LINK)
        val destination = PushDestinations.linkDestinationOf("$ORIGIN/channel/@ada")
        assertThat(destination?.entityId).isEqualTo("ada")
        assertThat(destination!!.type).isIn(PushDestinations.HANDLE_LINK_TYPES)
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/channel/")).isNull()
    }

    @Test
    fun `a sound link is a sound destination, never a reel`() {
        val destination = PushDestinations.linkDestinationOf("$ORIGIN/reels/sound/snd_1?utm_source=share")
        assertThat(destination?.type).isEqualTo(PushDestinations.TYPE_SOUND_LINK)
        assertThat(destination?.entityId).isEqualTo("snd_1")
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/reels/sound/")).isNull()
    }

    @Test
    fun `a hashtag link is a hashtag destination carrying the bare tag`() {
        val destination = PushDestinations.linkDestinationOf("$ORIGIN/hashtag/%23sunrise")
        assertThat(destination?.type).isEqualTo(PushDestinations.TYPE_HASHTAG_LINK)
        assertThat(destination?.entityId).isEqualTo("sunrise")
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/hashtag/sun%20rise")).isNull()
    }

    @Test
    fun `the shop route routes nowhere while commerce is paused`() {
        assertThat(PushDestinations.linkDestinationOf("$ORIGIN/shop/products/$ID")).isNull()
    }

    @Test
    fun `every destination type is distinct, so the nav host cannot confuse two routes`() {
        val types = listOf(
            PushDestinations.TYPE_POST_LINK,
            PushDestinations.TYPE_REEL_LINK,
            PushDestinations.TYPE_WATCH_LINK,
            PushDestinations.TYPE_PROFILE_LINK,
            PushDestinations.TYPE_CHANNEL_LINK,
            PushDestinations.TYPE_SOUND_LINK,
            PushDestinations.TYPE_HASHTAG_LINK,
            PushDestinations.TYPE_CHAT_JOIN,
        )
        assertThat(types.toSet()).hasSize(types.size)
    }

    private companion object {
        const val ORIGIN = "https://cleestudio.com"
        const val ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }
}
