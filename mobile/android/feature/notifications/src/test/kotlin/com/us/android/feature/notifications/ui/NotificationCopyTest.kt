package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * The sentence an inbox row shows for "{creator} is live" (2026-10-02).
 *
 * Without its own line the row read "You have a new notification", which
 * says nothing about a stream that is on right now. The sentence starts with
 * the actor so the row can set the name in bold, like every other row.
 */
class NotificationCopyTest {

    private fun row(kind: NotificationKind, actorName: String) = Notification(
        id = "n1",
        bucket = 1,
        ts = "ts",
        kind = kind,
        actorUserId = "u1",
        actorName = actorName,
        entityType = "live_stream",
        entityId = STREAM,
        target = NotificationTarget.Live(STREAM),
        isRead = false,
        createdAt = "2026-10-02T10:00:00Z",
    )

    @Test
    fun `a went-live row names the creator`() {
        assertThat(row(NotificationKind.CreatorWentLive, "Asha").describe()).isEqualTo("Asha is live")
    }

    @Test
    fun `a went-live row with no hydrated name says Someone`() {
        assertThat(row(NotificationKind.CreatorWentLive, "").describe()).isEqualTo("Someone is live")
    }

    @Test
    fun `the upload rows and the unknown row keep their wording`() {
        assertThat(row(NotificationKind.CreatorUploadedVideo, "Asha").describe()).isEqualTo("Asha uploaded a new video")
        assertThat(row(NotificationKind.Unknown("live_gift"), "Asha").describe())
            .isEqualTo("You have a new notification")
    }

    private companion object {
        const val STREAM = "3f2b8c1e-7a4d-4e9b-9c55-0d1e2f3a4b5c"
    }
}
