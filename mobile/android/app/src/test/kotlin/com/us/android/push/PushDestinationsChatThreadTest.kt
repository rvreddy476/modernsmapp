package com.us.android.push

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A chat push opens its thread, and a GROUP message opens it as a group.
 * notification-service sends every chat message as `dm`; only the deep link
 * (`/messenger?lane=groups&group=<id>`) marks a group.
 */
class PushDestinationsChatThreadTest {

    @Test
    fun `a direct message push opens its conversation as a direct chat`() {
        val thread = PushDestinations.chatThreadOf(dm(entityId = "conv-1", deepLink = "/messenger?user=sender-9"))

        assertThat(thread).isEqualTo(ChatPushThread("conv-1", isGroup = false))
    }

    @Test
    fun `a group message push opens its conversation as a group`() {
        val push = dm(entityId = "grp-7", deepLink = "/messenger?lane=groups&group=grp-7")

        val thread = PushDestinations.chatThreadOf(push)

        assertThat(thread).isEqualTo(ChatPushThread("grp-7", isGroup = true))
    }

    @Test
    fun `the group link stands in when the push carries no conversation id`() {
        val thread = PushDestinations.chatThreadOf(dm(entityId = " ", deepLink = "/messenger?group=grp-7&lane=groups"))

        assertThat(thread).isEqualTo(ChatPushThread("grp-7", isGroup = true))
    }

    @Test
    fun `a push with no conversation and no group link opens nothing`() {
        assertThat(PushDestinations.chatThreadOf(dm(entityId = "", deepLink = ""))).isNull()
        assertThat(PushDestinations.chatThreadOf(dm(entityId = "", deepLink = "/messenger?user=sender-9"))).isNull()
    }

    @Test
    fun `only the messenger's groups lane marks a group`() {
        assertThat(PushDestinations.messengerGroupIdOf("/messenger?lane=groups&group=g1#top")).isEqualTo("g1")
        assertThat(PushDestinations.messengerGroupIdOf("/messenger/?lane=groups&group=g1")).isEqualTo("g1")
        assertThat(PushDestinations.messengerGroupIdOf("/messenger?lane=channels&channel=g1")).isNull()
        assertThat(PushDestinations.messengerGroupIdOf("/messenger?lane=requests")).isNull()
        assertThat(PushDestinations.messengerGroupIdOf("/messenger?lane=groups&group=")).isNull()
        assertThat(PushDestinations.messengerGroupIdOf("/elsewhere?lane=groups&group=g1")).isNull()
        assertThat(PushDestinations.messengerGroupIdOf(null)).isNull()
    }

    private fun dm(entityId: String, deepLink: String) =
        PushDestination(type = "dm", entityId = entityId, deepLink = deepLink)
}
