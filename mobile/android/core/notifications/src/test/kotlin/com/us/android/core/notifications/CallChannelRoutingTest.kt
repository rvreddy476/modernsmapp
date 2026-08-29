package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Channel routing for call pushes. The spec's original aliases ("call",
 * "call_invite") were never what notification-service emits — the call
 * consumer sends `incoming_call` / `incoming_video_call` / `missed_call`
 * (call_consumer.go), so ringing pushes silently landed on the muteable
 * SOCIAL channel with no full-screen entitlement. This pins the REAL types.
 */
class CallChannelRoutingTest {

    @Test
    fun `the types the call consumer actually emits route to CALLS`() {
        for (type in listOf("incoming_call", "incoming_video_call", "missed_call")) {
            assertThat(NotificationChannelSpec.forType(type))
                .isEqualTo(NotificationChannelSpec.CALLS)
        }
    }

    @Test
    fun `chat types stay on MESSAGES and unknowns stay on SOCIAL`() {
        assertThat(NotificationChannelSpec.forType("dm"))
            .isEqualTo(NotificationChannelSpec.MESSAGES)
        assertThat(NotificationChannelSpec.forType("something_new"))
            .isEqualTo(NotificationChannelSpec.SOCIAL)
    }
}
