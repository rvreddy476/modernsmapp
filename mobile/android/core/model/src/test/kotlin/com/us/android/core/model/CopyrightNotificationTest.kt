package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Copyright-review notifications (2026-10-24): the three types
 * (notification-service copyright_notify.go:32-34) and where each lands —
 * a hold or a block on `/posttube/hub` (copyright_notify.go:46), a clear on
 * the video's watch page (copyright_consumer.go:37-41, upload_deeplink.go:29).
 */
class CopyrightNotificationTest {

    @Test
    fun `the three types read as their kinds`() {
        assertThat(NotificationKind.fromWire("copyright_review_hold")).isEqualTo(NotificationKind.CopyrightReviewHold)
        assertThat(NotificationKind.fromWire("copyright_review_cleared")).isEqualTo(NotificationKind.CopyrightReviewCleared)
        assertThat(NotificationKind.fromWire("copyright_review_blocked")).isEqualTo(NotificationKind.CopyrightReviewBlocked)
        assertThat(NotificationTarget.COPYRIGHT_TYPES)
            .containsExactly("copyright_review_hold", "copyright_review_cleared", "copyright_review_blocked")
    }

    @Test
    fun `the hub link opens the uploads, and the watch link the video`() {
        assertThat(NotificationTarget.parse("/posttube/hub")).isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(NotificationTarget.parse("/posttube/hub?tab=videos")).isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(NotificationTarget.parse("/tube/watch/p1")).isEqualTo(NotificationTarget.Video("p1"))
        // Nothing near it resolves to the hub by accident.
        assertThat(NotificationTarget.parse("/posttube/hub/extra")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("/tube/hub")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("posttube/hub")).isEqualTo(NotificationTarget.None)
        assertThat(NotificationTarget.parse("https://cleestudio.com/posttube/hub")).isEqualTo(NotificationTarget.None)
    }

    @Test
    fun `with no link, a hold or a block opens the uploads and a clear the video`() {
        assertThat(NotificationTarget.copyrightFallback("copyright_review_hold", "p1"))
            .isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(NotificationTarget.copyrightFallback("copyright_review_blocked", "p1"))
            .isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(NotificationTarget.copyrightFallback("copyright_review_cleared", " p1 "))
            .isEqualTo(NotificationTarget.Video("p1"))
        assertThat(NotificationTarget.copyrightFallback("copyright_review_cleared", ""))
            .isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(NotificationTarget.copyrightFallback("comment", "p1")).isEqualTo(NotificationTarget.None)
    }
}
