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

    // ── A followed creator went live (2026-10-02) ───────────────────────

    @Test
    fun `a live deep link opens the live viewer, and wins over the entity id`() {
        for (link in listOf("/posttube/live/$STREAM", "/reels/live/$STREAM", "/live/$STREAM")) {
            val destination = PushDestination(TYPE_WENT_LIVE, entityId = OTHER_STREAM, deepLink = link)
            assertThat(pushTargetOf(destination)).isEqualTo(NotificationTarget.Live(STREAM))
        }
    }

    @Test
    fun `a went-live push with no usable link falls back to its entity id`() {
        for (link in listOf("", "not a link", "/posttube/live", "https://evil.example/live/$OTHER_STREAM")) {
            val destination = PushDestination(TYPE_WENT_LIVE, entityId = STREAM, deepLink = link)
            assertThat(pushTargetOf(destination)).isEqualTo(NotificationTarget.Live(STREAM))
        }
    }

    /** The id goes into the join request: only a UUID is let through, link or no link. */
    @Test
    fun `a went-live push whose entity id is not a uuid opens nothing`() {
        for (id in listOf("", " ", "stream-1", "../$STREAM")) {
            assertThat(pushTargetOf(PushDestination(TYPE_WENT_LIVE, entityId = id, deepLink = "")))
                .isEqualTo(NotificationTarget.None)
        }
    }

    /** Only the went-live type may turn a bare entity id into a stream. */
    @Test
    fun `the live fallback belongs to the went-live type alone`() {
        assertThat(pushTargetOf(PushDestination("live_gift", entityId = STREAM, deepLink = "")))
            .isEqualTo(NotificationTarget.None)
        assertThat(pushTargetOf(PushDestination("commerce.order.shipped", entityId = STREAM, deepLink = "")))
            .isEqualTo(NotificationTarget.None)
        assertThat(pushTargetOf(PushDestination(TYPE_UPLOADED_VIDEO, entityId = STREAM, deepLink = "")))
            .isEqualTo(NotificationTarget.Video(STREAM))
    }

    @Test
    fun `the went-live type is the one notification-service sends`() {
        assertThat(TYPE_WENT_LIVE).isEqualTo("creator_went_live")
    }

    // ── How the viewer opens ────────────────────────────────────────────

    @Test
    fun `from any other screen the live viewer is pushed`() {
        assertThat(liveOpenOf(watching = null, streamId = STREAM)).isEqualTo(LiveOpen.PUSH)
    }

    /** A second viewer of the same room from one account is at best a reconnect. */
    @Test
    fun `a tap for the stream already on screen does nothing`() {
        assertThat(liveOpenOf(watching = STREAM, streamId = STREAM)).isEqualTo(LiveOpen.STAY)
        assertThat(liveOpenOf(watching = STREAM, streamId = STREAM.uppercase())).isEqualTo(LiveOpen.STAY)
    }

    /** Stacking would leave the first stream connected, and audible, underneath. */
    @Test
    fun `a tap for a different stream replaces the viewer`() {
        assertThat(liveOpenOf(watching = OTHER_STREAM, streamId = STREAM)).isEqualTo(LiveOpen.REPLACE)
    }

    private companion object {
        const val STREAM = "3f2b8c1e-7a4d-4e9b-9c55-0d1e2f3a4b5c"
        const val OTHER_STREAM = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
    }
}
