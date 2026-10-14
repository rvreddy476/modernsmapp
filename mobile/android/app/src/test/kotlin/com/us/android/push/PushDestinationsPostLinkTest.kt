package com.us.android.push

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.WebLinks
import org.junit.Test

/**
 * The post permalink App Link (T-07): `https://cleestudio.com/post/{id}` routes to
 * the post detail; anything else that is not the invite link routes nowhere.
 */
class PushDestinationsPostLinkTest {

    @Test
    fun `a post permalink becomes a post destination carrying the id`() {
        val link = "https://cleestudio.com/post/$POST_ID"
        val destination = PushDestinations.linkDestinationOf(link)

        assertThat(destination).isEqualTo(
            PushDestination(type = PushDestinations.TYPE_POST_LINK, entityId = POST_ID, deepLink = link),
        )
    }

    @Test
    fun `the link Copy link and Share hand out is the link the app opens`() {
        val destination = PushDestinations.linkDestinationOf(WebLinks.postPermalink(POST_ID))
        assertThat(destination?.type).isEqualTo(PushDestinations.TYPE_POST_LINK)
        assertThat(destination?.entityId).isEqualTo(POST_ID)
    }

    @Test
    fun `query, fragment and a trailing slash do not change the post`() {
        assertThat(PushDestinations.linkDestinationOf("https://cleestudio.com/post/$POST_ID/?utm=x#c")?.entityId)
            .isEqualTo(POST_ID)
    }

    @Test
    fun `a bad post id routes nowhere`() {
        listOf(
            "https://cleestudio.com/post/",
            "https://cleestudio.com/post/123",
            "https://cleestudio.com/post/not-a-uuid",
            "https://cleestudio.com/post/$POST_ID/extra",
            "https://cleestudio.com/post/%2E%2E",
        ).forEach { assertThat(PushDestinations.linkDestinationOf(it)).isNull() }
    }

    @Test
    fun `other hosts, schemes and the old p route route nowhere`() {
        listOf(
            null,
            "",
            "http://cleestudio.com/post/$POST_ID",
            "https://evil.example/post/$POST_ID",
            "https://cleestudio.com.evil.example/post/$POST_ID",
            "https://cleestudio.com/p/$POST_ID",
            "https://momentum.app/p/$POST_ID",
        ).forEach { assertThat(PushDestinations.linkDestinationOf(it)).isNull() }
    }

    @Test
    fun `the invite link still routes to the join preview`() {
        val destination = PushDestinations.linkDestinationOf("https://atpost.app/chat/join/k7Qm")
        assertThat(destination?.type).isEqualTo(PushDestinations.TYPE_CHAT_JOIN)
        assertThat(destination?.entityId).isEqualTo("k7Qm")
    }

    private companion object {
        const val POST_ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }
}
