package com.us.android.core.notifications

import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.data.NotificationShadeTeardown
import kotlinx.coroutines.test.runTest
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

/**
 * Chat audit L3: notifications already in the shade belong to the account
 * that received them. Sign-out clears them, so the next account on this
 * handset cannot tap the previous one's "New Message" into its conversation.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class NotificationShadeTeardownTest {

    private lateinit var context: Context
    private lateinit var manager: NotificationManager

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        shadowOf(context.applicationContext as android.app.Application)
            .grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.MOMENTUM)
        manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    }

    @Test
    fun `sign-out clears every notification the previous account received`() = runTest {
        val presenter = NotificationPresenter(context, AppForegroundState(), DoorstepOfferAlertCoordinator())
        presenter.present(push(type = "dm", entityId = "conv-a"))
        presenter.present(push(type = "follow", entityId = "user-b"))
        assertThat(shadowOf(manager).allNotifications).hasSize(2)

        NotificationShadeTeardown(context).onSignOut()

        assertThat(shadowOf(manager).allNotifications).isEmpty()
    }

    @Test
    fun `sign-out with an empty shade is a no-op`() = runTest {
        NotificationShadeTeardown(context).onSignOut()

        assertThat(shadowOf(manager).allNotifications).isEmpty()
    }

    private fun push(type: String, entityId: String) = mapOf(
        NotificationPresenter.KEY_TITLE to "New",
        NotificationPresenter.KEY_TYPE to type,
        NotificationPresenter.KEY_ENTITY_ID to entityId,
    )
}
