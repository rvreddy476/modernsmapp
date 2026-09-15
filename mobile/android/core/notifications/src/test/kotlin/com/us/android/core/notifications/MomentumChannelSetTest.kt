package com.us.android.core.notifications

import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Per-app notification channels (Feast A0, 2026-09-13).
 *
 * `createAll` used to register every enum entry. With Feast Kitchen and Feast
 * Rider as separate installs, each app now passes its own set, and Momentum's
 * set must stay EXACTLY what the app registered before the split: a missing id
 * means a push posted to it is dropped silently, and an extra one is a switch
 * in system settings for something the app never sends.
 *
 * The ids are written out literally, not read back from the enum, so the test
 * fails when either the set or an id changes — a renamed id orphans the user's
 * existing preference.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MomentumChannelSetTest {

    @Test
    fun `Momentum's set is exactly today's six channels`() {
        // "dating" joined on 2026-09-16 (Wave 3): Dating ships only in Momentum.
        assertThat(NotificationChannelSpec.MOMENTUM.map { it.id })
            .containsExactly("calls", "messages", "social", "new_videos", "account", "dating")
    }

    @Test
    fun `createAll registers exactly the set it is given and nothing else`() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

        NotificationChannelSpec.createAll(context, setOf(NotificationChannelSpec.ACCOUNT))
        assertThat(manager.notificationChannels.map { it.id }).containsExactly("account")

        NotificationChannelSpec.createAll(context, NotificationChannelSpec.MOMENTUM)
        assertThat(manager.notificationChannels.map { it.id })
            .containsExactly("calls", "messages", "social", "new_videos", "account", "dating")
    }
}
