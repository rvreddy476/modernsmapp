package com.us.android.push

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The group invite App Link — `https://atpost.app/chat/join/{code}` — and nothing else, yields a code. */
class PushDestinationsLinkTest {

    @Test
    fun `the join link yields its code`() {
        assertThat(PushDestinations.joinCodeOf("https://atpost.app/chat/join/k7Qm")).isEqualTo("k7Qm")
        assertThat(PushDestinations.joinCodeOf("https://ATPOST.app/chat/join/k7Qm/")).isEqualTo("k7Qm")
        assertThat(PushDestinations.joinCodeOf("https://atpost.app/chat/join/k7Qm?utm=x#top")).isEqualTo("k7Qm")
    }

    @Test
    fun `other links, hosts, paths and schemes yield nothing`() {
        assertThat(PushDestinations.joinCodeOf(null)).isNull()
        assertThat(PushDestinations.joinCodeOf("")).isNull()
        assertThat(PushDestinations.joinCodeOf("http://atpost.app/chat/join/k7Qm")).isNull()
        assertThat(PushDestinations.joinCodeOf("https://evil.example/chat/join/k7Qm")).isNull()
        assertThat(PushDestinations.joinCodeOf("https://atpost.app/chat/join/")).isNull()
        assertThat(PushDestinations.joinCodeOf("https://atpost.app/p/123")).isNull()
        assertThat(PushDestinations.joinCodeOf("https://atpost.app/chat/join/a/b")).isNull()
    }

    @Test
    fun `a chat join destination carries the code as its entity`() {
        val destination = PushDestination(type = PushDestinations.TYPE_CHAT_JOIN, entityId = "k7Qm", deepLink = "x")
        assertThat(destination.type).isEqualTo("chat_join")
        assertThat(destination.entityId).isEqualTo("k7Qm")
    }
}
