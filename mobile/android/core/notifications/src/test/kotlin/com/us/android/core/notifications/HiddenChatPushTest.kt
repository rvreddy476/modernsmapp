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
 * Hidden chats (24 Oct 2026): a message push for a chat the recipient hid
 * shows "New message" and nothing else — whatever arrives — taps into the
 * app rather than the chat, and folds into one notification.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class HiddenChatPushTest {

    private lateinit var context: Context
    private lateinit var presenter: NotificationPresenter
    private lateinit var manager: NotificationManager

    /** Even a push that (wrongly) carried names and ids must not show them. */
    private val leaky = mapOf(
        NotificationPresenter.KEY_TYPE to "dm",
        NotificationPresenter.KEY_TITLE to "Ana",
        NotificationPresenter.KEY_BODY to "see you at 8",
        NotificationPresenter.KEY_CONVERSATION_ID to "conv-secret",
        NotificationPresenter.KEY_ENTITY_ID to "conv-secret",
        NotificationPresenter.KEY_DEEP_LINK to "/messages/conv-secret",
        "sender_name" to "Ana",
        "message_id" to "m1",
        KEY_HIDDEN_CHAT to "true",
    )

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
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
        presenter = NotificationPresenter(context, AppForegroundState(), DoorstepOfferAlertCoordinator())
        manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    }

    @Test
    fun `the sanitised push keeps nothing but New message`() {
        val safe = hiddenChatSafePush(leaky)
        assertThat(safe[NotificationPresenter.KEY_TITLE]).isEqualTo("New message")
        assertThat(safe[NotificationPresenter.KEY_BODY]).isEmpty()
        assertThat(safe.keys).containsNoneOf(
            NotificationPresenter.KEY_CONVERSATION_ID,
            NotificationPresenter.KEY_ENTITY_ID,
            NotificationPresenter.KEY_DEEP_LINK,
            "sender_name",
            "message_id",
        )
        assertThat(safe.values.joinToString()).doesNotContain("Ana")
        assertThat(safe.values.joinToString()).doesNotContain("conv-secret")
    }

    @Test
    fun `a normal push and a ringing push are untouched`() {
        val normal = leaky - KEY_HIDDEN_CHAT
        assertThat(hiddenChatSafePush(normal)).isSameInstanceAs(normal)
        val ring = mapOf(NotificationPresenter.KEY_TYPE to "incoming_call", NotificationPresenter.KEY_ENTITY_ID to "call-1", KEY_HIDDEN_CHAT to "true")
        assertThat(hiddenChatSafePush(ring)).isSameInstanceAs(ring)
    }

    @Test
    fun `a hidden chat's push renders New message and taps into the app, not the chat`() {
        presenter.present(leaky)

        val posted = shadowOf(manager).allNotifications.single()
        assertThat(posted.extras.getString(android.app.Notification.EXTRA_TITLE)).isEqualTo("New message")
        assertThat(posted.extras.getCharSequence(android.app.Notification.EXTRA_TEXT)?.toString().orEmpty()).isEmpty()
        val launched = shadowOf(posted.contentIntent).savedIntent
        assertThat(launched.getStringExtra(NotificationPresenter.KEY_ENTITY_ID)).isNull()
        assertThat(launched.getStringExtra(NotificationPresenter.KEY_DEEP_LINK)).isNull()
    }

    @Test
    fun `every hidden chat's messages fold into one notification`() {
        presenter.present(leaky)
        presenter.present(leaky + (NotificationPresenter.KEY_CONVERSATION_ID to "conv-other"))

        assertThat(shadowOf(manager).allNotifications).hasSize(1)
    }

    @Test
    fun `a hidden chat's push is never acked as a chat message`() {
        assertThat(chatPushTarget(hiddenChatSafePush(leaky))).isNull()
    }
}
