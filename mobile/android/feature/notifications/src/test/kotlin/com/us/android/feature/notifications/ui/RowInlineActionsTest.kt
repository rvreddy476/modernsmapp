package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationKind
import org.junit.Test

/** Which buttons an inbox row carries. Connect is gone (founder, 24 Oct 2026). */
class RowInlineActionsTest {

    @Test
    fun `message and follow requests keep their answers, a follow keeps Follow back`() {
        assertThat(inlineActionsFor(NotificationKind.MessageRequest)).isEqualTo(RowInlineActions.MessageRequest)
        assertThat(inlineActionsFor(NotificationKind.FollowRequest)).isEqualTo(RowInlineActions.FollowRequest)
        assertThat(inlineActionsFor(NotificationKind.Follow)).isEqualTo(RowInlineActions.FollowBack)
    }

    @Test
    fun `an old connection request or acceptance offers nothing to press`() {
        assertThat(inlineActionsFor(NotificationKind.ConnectionRequest)).isNull()
        assertThat(inlineActionsFor(NotificationKind.ConnectionAccepted)).isNull()
        assertThat(RowInlineActions.entries.map { it.name }.filter { it.contains("Connection") }).isEmpty()
    }

    @Test
    fun `the old request row says connections have ended, in the web's words`() {
        assertThat(CONNECTIONS_ENDED).isEqualTo("Connections have ended. Follow or message them instead.")
    }
}
