package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * Membership pushes (paid memberships, 2026-10-23) post to the Account
 * channel, as notification-service names it (membership_notify.go
 * `MembershipAndroidChannel = "account"`) — not Social, so muting likes never
 * mutes "your membership ends today".
 */
class MembershipChannelTest {

    @Test
    fun `every membership type is an account notice`() {
        NotificationTarget.MEMBERSHIP_TYPES.forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.ACCOUNT)
        }
        assertThat(
            NotificationChannelSpec.forType("membership_something_new")
        ).isNotEqualTo(NotificationChannelSpec.ACCOUNT)
    }
}
