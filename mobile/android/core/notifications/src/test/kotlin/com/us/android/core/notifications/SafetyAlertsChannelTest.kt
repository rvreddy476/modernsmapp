package com.us.android.core.notifications

import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Safety alerts (founder decision, 8 Nov 2026): a panic page is on its own
 * HIGH channel, apart from Dating, so someone who mutes Dating still sees a
 * friend asking for help. Private on the lock screen, with a public version
 * that says there is an alert but never who.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SafetyAlertsChannelTest {

    @Test
    fun `panic pages land on safety alerts, every other dating push stays on dating`() {
        listOf("dating.safety.panic.trusted_contact", "dating.safety.panic.responder").forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.SAFETY_ALERTS)
            assertThat(NotificationChannelSpec.forPush(type, null, "safety_alerts"))
                .isEqualTo(NotificationChannelSpec.SAFETY_ALERTS)
        }
        listOf(
            "dating.safety.location_shared",
            "dating.safety.panic.acknowledged",
            "dating_scam_alert",
            "dating.match.formed",
        ).forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.DATING)
        }
    }

    @Test
    fun `only Momentum registers it, HIGH and private on the lock screen`() {
        assertThat(NotificationChannelSpec.MOMENTUM).contains(NotificationChannelSpec.SAFETY_ALERTS)
        assertThat(NotificationChannelSpec.KITCHEN).doesNotContain(NotificationChannelSpec.SAFETY_ALERTS)
        assertThat(NotificationChannelSpec.RIDER).doesNotContain(NotificationChannelSpec.SAFETY_ALERTS)

        val context = ApplicationProvider.getApplicationContext<Context>()
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.MOMENTUM)
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val channel = checkNotNull(manager.getNotificationChannel("safety_alerts"))
        assertThat(channel.importance).isEqualTo(NotificationManager.IMPORTANCE_HIGH)
        assertThat(channel.lockscreenVisibility).isEqualTo(Notification.VISIBILITY_PRIVATE)
    }

    @Test
    fun `a panic alert is private itself, and its lock-screen version names nobody`() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        shadowOf(context.applicationContext as android.app.Application)
            .grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        val launcher = android.content.ComponentName(context.packageName, "com.us.android.MainActivity")
        shadowOf(context.packageManager).addActivityIfNotPresent(launcher)
        shadowOf(context.packageManager).addIntentFilterForActivity(
            launcher,
            android.content.IntentFilter(android.content.Intent.ACTION_MAIN).apply {
                addCategory(android.content.Intent.CATEGORY_LAUNCHER)
            },
        )
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.MOMENTUM)

        val foreground = AppForegroundState().apply { isForeground = false }
        NotificationPresenter(context, foreground, DoorstepOfferAlertCoordinator()).present(
            mapOf(
                NotificationPresenter.KEY_TITLE to "Priya triggered a safety alert",
                NotificationPresenter.KEY_BODY to "Check on them now",
                NotificationPresenter.KEY_TYPE to "dating.safety.panic.trusted_contact",
                NotificationPresenter.KEY_ENTITY_ID to "8d0f7a8e-3c1b-4f53-9d2a-6e3b1f0c9a11",
                NotificationPresenter.KEY_DEEP_LINK to "/dating/safety/alerts/8d0f7a8e-3c1b-4f53-9d2a-6e3b1f0c9a11",
            ),
        )

        val nm = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val posted = shadowOf(nm).allNotifications.single()
        assertThat(posted.channelId).isEqualTo("safety_alerts")
        assertThat(posted.visibility).isEqualTo(Notification.VISIBILITY_PRIVATE)
        assertThat(posted.category).isEqualTo(Notification.CATEGORY_ALARM)
        val public = checkNotNull(posted.publicVersion)
        val publicTitle = public.extras.getCharSequence(Notification.EXTRA_TITLE).toString()
        val publicText = public.extras.getCharSequence(Notification.EXTRA_TEXT).toString()
        assertThat(publicTitle).isEqualTo(NotificationPresenter.SAFETY_PUBLIC_TITLE)
        assertThat(publicText).isEqualTo(NotificationPresenter.SAFETY_PUBLIC_TEXT)
        listOf("Priya", "Pulse", "dating", "date").forEach { word ->
            assertThat("$publicTitle $publicText").ignoringCase().doesNotContain(word)
        }
    }
}
