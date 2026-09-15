package com.us.android.core.notifications

import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Dating channel (Wave 3, 2026-09-16): its own switch, registered by
 * Momentum only, private on the lock screen, and the one every dating push
 * lands on — not SOCIAL, where muting likes would mute a match.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class DatingChannelTest {

    @Test
    fun `every dating push lands on the dating channel`() {
        listOf(
            "dating.spark.created",
            "dating.match.formed",
            "dating.match.new_message",
            "dating.match.first_message",
        ).forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.DATING)
        }
        assertThat(NotificationChannelSpec.forType("dm")).isEqualTo(NotificationChannelSpec.MESSAGES)
        assertThat(NotificationChannelSpec.forType("dating.something_new")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }

    @Test
    fun `only Momentum registers it, and it is private on the lock screen`() {
        assertThat(NotificationChannelSpec.MOMENTUM).contains(NotificationChannelSpec.DATING)
        assertThat(NotificationChannelSpec.KITCHEN).doesNotContain(NotificationChannelSpec.DATING)
        assertThat(NotificationChannelSpec.RIDER).doesNotContain(NotificationChannelSpec.DATING)

        val context = ApplicationProvider.getApplicationContext<Context>()
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.MOMENTUM)
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val channel = checkNotNull(manager.getNotificationChannel("dating"))
        assertThat(channel.importance).isEqualTo(NotificationManager.IMPORTANCE_DEFAULT)
        assertThat(channel.lockscreenVisibility).isEqualTo(Notification.VISIBILITY_PRIVATE)
    }
}
