package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationIcon
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * The copyright-review rows (2026-10-24): the server's inbox title first,
 * else its template titles from the type (notification-service
 * templates.go:312-326). The author is the actor of their own notice
 * (copyright_notify.go:96-98), so the row draws a glyph, never the
 * viewer's own face.
 */
class CopyrightCopyTest {

    private fun row(kind: NotificationKind, title: String? = null, icon: NotificationIcon? = null) = Notification(
        id = "n1",
        bucket = 1,
        ts = "ts",
        kind = kind,
        actorUserId = "a0000000-0000-4000-8000-0000000000aa",
        actorName = "Kiran",
        entityType = "post",
        entityId = "p1",
        target = NotificationTarget.CreatorHubUploads,
        isRead = false,
        createdAt = "2026-10-24T08:00:02Z",
        title = title,
        icon = icon,
    )

    @Test
    fun `the server's title is the row`() {
        assertThat(row(NotificationKind.CopyrightReviewHold, title = "Your video is in copyright review").describe())
            .isEqualTo("Your video is in copyright review")
    }

    @Test
    fun `without a title each type says the server's template title`() {
        assertThat(row(NotificationKind.CopyrightReviewHold).describe()).isEqualTo("Your video is in copyright review")
        assertThat(row(NotificationKind.CopyrightReviewCleared).describe()).isEqualTo("Your video cleared copyright review")
        assertThat(row(NotificationKind.CopyrightReviewBlocked).describe())
            .isEqualTo("Your video was blocked after copyright review")
        assertThat(copyrightLine(NotificationKind.Comment)).isEqualTo("You have a new notification")
    }

    @Test
    fun `the row draws a glyph even though the actor is named`() {
        val hold = row(NotificationKind.CopyrightReviewHold)
        assertThat(hold.hasActor).isTrue()
        assertThat(hold.rowAvatar()).isEqualTo(RowAvatar.Glyph(NotificationIcon.System))
        assertThat(row(NotificationKind.CopyrightReviewBlocked, icon = NotificationIcon.Account).rowAvatar())
            .isEqualTo(RowAvatar.Glyph(NotificationIcon.Account))
        // Anyone else's row with a name still shows the person.
        assertThat(row(NotificationKind.Comment).rowAvatar()).isEqualTo(RowAvatar.Actor)
    }
}
