package com.us.android.push

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * What an upload push opens (Tube subscriptions, 2026-09-12): the deep
 * link when it parses, the entity id by type when it does not, and nothing
 * for a type this build does not route.
 */
class PushTargetsTest {

    @Test
    fun `the deep link wins when it parses`() {
        val destination = PushDestination(type = TYPE_UPLOADED_VIDEO, entityId = "other", deepLink = "/tube/watch/p1")
        assertThat(pushTargetOf(destination)).isEqualTo(NotificationTarget.Video("p1"))
    }

    @Test
    fun `a reel link opens the reel whatever the type says`() {
        val destination = PushDestination(type = TYPE_UPLOADED_VIDEO, entityId = "p9", deepLink = "/reels/p2")
        assertThat(pushTargetOf(destination)).isEqualTo(NotificationTarget.Reel("p2"))
    }

    @Test
    fun `the legacy posttube link still opens the watch screen`() {
        val destination = PushDestination(type = TYPE_UPLOADED_VIDEO, entityId = "", deepLink = "/posttube/watch/p1")
        assertThat(pushTargetOf(destination)).isEqualTo(NotificationTarget.Video("p1"))
    }

    @Test
    fun `with no usable link the entity id and type decide`() {
        assertThat(pushTargetOf(PushDestination(TYPE_UPLOADED_VIDEO, entityId = "p1", deepLink = "")))
            .isEqualTo(NotificationTarget.Video("p1"))
        assertThat(pushTargetOf(PushDestination(TYPE_UPLOADED_FLICK, entityId = "p2", deepLink = "not a link")))
            .isEqualTo(NotificationTarget.Reel("p2"))
    }

    @Test
    fun `a host-prefixed link is not trusted and falls back to the id`() {
        val destination = PushDestination(TYPE_UPLOADED_FLICK, entityId = "p2", deepLink = "https://evil.example/reels/x")
        assertThat(pushTargetOf(destination)).isEqualTo(NotificationTarget.Reel("p2"))
    }

    @Test
    fun `no link and no id, or an unknown type, opens nothing`() {
        assertThat(pushTargetOf(PushDestination(TYPE_UPLOADED_VIDEO, entityId = " ", deepLink = "")))
            .isEqualTo(NotificationTarget.None)
        assertThat(pushTargetOf(PushDestination("commerce.order.shipped", entityId = "o1", deepLink = "")))
            .isEqualTo(NotificationTarget.None)
    }
}
