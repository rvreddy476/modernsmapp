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
 * The Dating channel (Wave 3, 2026-09-16): its own switch, registered by
 * Momentum only, private on the lock screen, and the one every dating push
 * lands on — not SOCIAL, where muting likes would mute a match.
 *
 * Dating audit M2 (30 Oct): EVERY `dating.` / `dating_` type lands here — the
 * check-in and scam alert used to fall to SOCIAL, which is not private — and
 * each dating notification is itself PRIVATE with a public version that names
 * nobody.
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
            "dating_date_checkin",
            "dating_scam_alert",
            "dating.safety.location_shared",
            // A dating type this build does not know yet is still private.
            "dating.something_new",
            "dating_something_new",
        ).forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.DATING)
            assertThat(NotificationChannelSpec.forPush(type, null, "dating")).isEqualTo(NotificationChannelSpec.DATING)
        }
        assertThat(NotificationChannelSpec.forType("dm")).isEqualTo(NotificationChannelSpec.MESSAGES)
        // Only the prefix with its separator: a word that merely starts with "dating" is not a dating push.
        assertThat(NotificationChannelSpec.forType("datingish")).isEqualTo(NotificationChannelSpec.SOCIAL)
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

    @Test
    fun `a dating notification is private itself, and its lock-screen version names nobody`() {
        val (context, presenter) = presenterShade()

        presenter.present(
            mapOf(
                NotificationPresenter.KEY_TITLE to "How did it go?",
                NotificationPresenter.KEY_BODY to "Tell us how your date with Rahul went",
                NotificationPresenter.KEY_TYPE to "dating_date_checkin",
                NotificationPresenter.KEY_ENTITY_ID to "m-1",
                NotificationPresenter.KEY_DEEP_LINK to "/dating/matches/m-1?checkin=1",
            ),
        )

        val nm = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val posted = shadowOf(nm).allNotifications.single()
        assertThat(posted.channelId).isEqualTo(NotificationChannelSpec.DATING.id)
        assertThat(posted.visibility).isEqualTo(Notification.VISIBILITY_PRIVATE)
        val public = checkNotNull(posted.publicVersion)
        val publicTitle = public.extras.getCharSequence(Notification.EXTRA_TITLE).toString()
        val publicText = public.extras.getCharSequence(Notification.EXTRA_TEXT).toString()
        assertThat(publicTitle).isEqualTo(NotificationPresenter.DATING_PUBLIC_TITLE)
        assertThat(publicText).isEqualTo(NotificationPresenter.DATING_PUBLIC_TEXT)
        listOf("Rahul", "date", "Pulse", "dating").forEach { word ->
            assertThat(publicTitle + " " + publicText).ignoringCase().doesNotContain(word)
        }
    }

    @Test
    fun `a non-dating notification gets no dating public version`() {
        val (context, presenter) = presenterShade()

        presenter.present(
            mapOf(
                NotificationPresenter.KEY_TITLE to "New like",
                NotificationPresenter.KEY_BODY to "Someone liked your post",
                NotificationPresenter.KEY_TYPE to "like",
                NotificationPresenter.KEY_ENTITY_ID to "p-1",
            ),
        )

        val nm = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val posted = shadowOf(nm).allNotifications.single()
        assertThat(posted.channelId).isEqualTo(NotificationChannelSpec.SOCIAL.id)
        assertThat(posted.publicVersion).isNull()
    }

    /** A presenter that can post (permission granted, launcher present) in the background. */
    private fun presenterShade(): Pair<Context, NotificationPresenter> {
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
        return context to NotificationPresenter(context, foreground, DoorstepOfferAlertCoordinator())
    }
}
