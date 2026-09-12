package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Channel routing for upload pushes (Tube subscriptions, 2026-09-12).
 * Before the NEW_VIDEOS channel existed, `creator_uploaded_video` and
 * `creator_uploaded_flick` fell through to SOCIAL, so a subscriber who
 * muted likes muted uploads too. This pins both types to their own channel
 * and the channel's identity, because a changed id would orphan the user's
 * setting.
 */
class NewVideosChannelRoutingTest {

    @Test
    fun `both upload types route to NEW_VIDEOS`() {
        for (type in listOf("creator_uploaded_video", "creator_uploaded_flick")) {
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.NEW_VIDEOS)
        }
    }

    @Test
    fun `the channel's id and copy are the ones the settings screen names`() {
        val spec = NotificationChannelSpec.NEW_VIDEOS
        assertThat(spec.id).isEqualTo("new_videos")
        assertThat(spec.title).isEqualTo("New videos")
        assertThat(spec.description).isEqualTo("Uploads from channels you subscribe to")
        assertThat(spec.importance).isEqualTo(android.app.NotificationManager.IMPORTANCE_DEFAULT)
    }

    @Test
    fun `social types are untouched`() {
        assertThat(NotificationChannelSpec.forType("reaction")).isEqualTo(NotificationChannelSpec.SOCIAL)
        assertThat(NotificationChannelSpec.forType(null)).isEqualTo(NotificationChannelSpec.SOCIAL)
    }
}
