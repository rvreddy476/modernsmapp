package com.us.android.core.notifications

import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Privacy-safe chat rendering and the tap contract (completion pass, scope H).
 *
 * The payload the server sends for chat is GENERIC ("New Message", no text,
 * no sender) — these tests pin the client half: routing to the Messages
 * channel, the deep-link extras a tap carries, suppression while the app is
 * foreground (the socket already delivered), and clearing once the
 * conversation is handled.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatNotificationPrivacyTest {

    private lateinit var context: Context
    private lateinit var foreground: AppForegroundState
    private lateinit var presenter: NotificationPresenter
    private lateinit var manager: NotificationManager

    private val dmPush = mapOf(
        NotificationPresenter.KEY_TITLE to "New Message",
        NotificationPresenter.KEY_BODY to "You have a new message",
        NotificationPresenter.KEY_TYPE to "dm",
        NotificationPresenter.KEY_ENTITY_ID to "conv-42",
        NotificationPresenter.KEY_DEEP_LINK to "/messages/conv-42",
    )

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        // POST_NOTIFICATIONS is a runtime permission from 13; ungranted, the
        // presenter (correctly) no-ops and every assertion sees an empty shade.
        shadowOf(context.applicationContext as android.app.Application)
            .grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        // The test manifest has no launcher activity; register one so
        // getLaunchIntentForPackage resolves the way it does on a device.
        val launcher = android.content.ComponentName(context.packageName, "com.us.android.MainActivity")
        shadowOf(context.packageManager).addActivityIfNotPresent(launcher)
        shadowOf(context.packageManager).addIntentFilterForActivity(
            launcher,
            android.content.IntentFilter(android.content.Intent.ACTION_MAIN).apply {
                addCategory(android.content.Intent.CATEGORY_LAUNCHER)
            },
        )
        NotificationChannelSpec.createAll(context)
        foreground = AppForegroundState()
        presenter = NotificationPresenter(context, foreground)
        manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    }

    @Test
    fun `a background dm push renders generic text on the Messages channel with routing extras`() {
        foreground.isForeground = false

        presenter.present(dmPush)

        val posted = shadowOf(manager).allNotifications.single()
        assertThat(posted.channelId).isEqualTo(NotificationChannelSpec.MESSAGES.id)
        assertThat(posted.extras.getString(android.app.Notification.EXTRA_TITLE))
            .isEqualTo("New Message")
        // Generic by construction — nothing resembling message content.
        assertThat(posted.extras.getString(android.app.Notification.EXTRA_TEXT))
            .isEqualTo("You have a new message")
        // The tap intent carries the routing triple MainActivity reads.
        val launched = shadowOf(posted.contentIntent).savedIntent
        assertThat(launched.getStringExtra(NotificationPresenter.KEY_TYPE)).isEqualTo("dm")
        assertThat(launched.getStringExtra(NotificationPresenter.KEY_ENTITY_ID)).isEqualTo("conv-42")
        assertThat(launched.getStringExtra(NotificationPresenter.KEY_DEEP_LINK))
            .isEqualTo("/messages/conv-42")
    }

    @Test
    fun `a foreground chat push is suppressed — the socket already delivered it`() {
        foreground.isForeground = true

        presenter.present(dmPush)

        assertThat(shadowOf(manager).allNotifications).isEmpty()
    }

    @Test
    fun `a foreground NON-chat push still renders`() {
        foreground.isForeground = true

        presenter.present(
            mapOf(
                NotificationPresenter.KEY_TITLE to "New Follower",
                NotificationPresenter.KEY_TYPE to "follow",
            ),
        )

        assertThat(shadowOf(manager).allNotifications).hasSize(1)
    }

    @Test
    fun `opening the conversation clears its notification`() {
        foreground.isForeground = false
        presenter.present(dmPush)
        assertThat(shadowOf(manager).allNotifications).hasSize(1)

        presenter.cancelForSubject("conv-42")

        assertThat(shadowOf(manager).allNotifications).isEmpty()
    }

    @Test
    fun `repeated pushes for one conversation replace instead of stacking`() {
        foreground.isForeground = false

        presenter.present(dmPush)
        presenter.present(dmPush)

        assertThat(shadowOf(manager).allNotifications).hasSize(1)
    }
}
