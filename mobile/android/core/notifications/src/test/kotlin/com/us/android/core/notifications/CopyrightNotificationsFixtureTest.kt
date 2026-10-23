package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.notifications.data.NotificationDto
import com.us.android.core.notifications.data.toDomain
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Test
import java.io.File

/**
 * Copyright title review's three inbox rows (2026-10-24). The fixture is
 * written from notification-service: the types (copyright_notify.go:32-34),
 * the links (a hold and a block `/posttube/hub`, copyright_notify.go:46; a
 * clear `/tube/watch/{id}`, copyright_consumer.go:37-41 and
 * upload_deeplink.go:29), the titles (templates.go:312-326) and the entity
 * (`post`, copyright_notify.go:40). The actor is the author themselves
 * (copyright_notify.go:96-98). Row 3 has no link: the type decides.
 */
class CopyrightNotificationsFixtureTest {

    private val app = NetworkModule.provideJson()
    private val rows = app.decodeFromString(
        ApiEnvelope.serializer(ListSerializer(NotificationDto.serializer())),
        File("src/test/resources/contracts/notifications_inbox_copyright.json").readText(),
    ).data.orEmpty().map { it.toDomain() }

    @Test
    fun `a hold opens the uploads, a clear the video, and a block with no link the uploads`() {
        val (hold, cleared, blocked) = rows
        assertThat(hold.kind).isEqualTo(NotificationKind.CopyrightReviewHold)
        assertThat(hold.target).isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(hold.title).isEqualTo("Your video is in copyright review")

        assertThat(cleared.kind).isEqualTo(NotificationKind.CopyrightReviewCleared)
        assertThat(cleared.target).isEqualTo(NotificationTarget.Video("7f1c2a90-0000-4000-8000-000000000001"))

        assertThat(blocked.kind).isEqualTo(NotificationKind.CopyrightReviewBlocked)
        assertThat(blocked.target).isEqualTo(NotificationTarget.CreatorHubUploads)
        assertThat(blocked.title).isNull()
    }
}
