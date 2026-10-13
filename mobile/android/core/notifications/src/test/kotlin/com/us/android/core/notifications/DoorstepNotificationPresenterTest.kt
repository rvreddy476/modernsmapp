package com.us.android.core.notifications

import android.Manifest
import android.app.Application
import android.app.Notification
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
import java.time.Instant

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class DoorstepNotificationPresenterTest {
    @Test fun `accepted practice clears tray offers and suppresses later pushes but not account updates`() {
        val inbox=PartnerOfferInbox()
        val p=NotificationPresenter(context,foreground,alerts,java.util.Optional.of(com.us.android.core.notifications.data.PushApp.PARTNER),inbox)
        fun data()=push()+mapOf("entity_id" to java.util.UUID.randomUUID().toString(),"test_only" to "true")
        repeat(5){p.present(data())}
        assertThat(shadowOf(manager).allNotifications.size).isEqualTo(5)
        inbox.accepted(true)
        p.cancelPartnerOffers(true)
        assertThat(shadowOf(manager).allNotifications).isEmpty()
        p.present(data())
        assertThat(shadowOf(manager).allNotifications).isEmpty()
        p.present(mapOf("type" to "doorstep.pro.job.cancelled","entity_id" to "update","title" to "Job update"))
        assertThat(shadowOf(manager).allNotifications.size).isEqualTo(1)
    }
    private lateinit var context: Context
    private lateinit var manager: NotificationManager
    private lateinit var presenter: NotificationPresenter
    private val foreground = AppForegroundState()
    private val alerts = DoorstepOfferAlertCoordinator()

    private fun push(expiry: Instant = Instant.now().plusSeconds(30)) = mapOf(
        "type" to "doorstep.pro.offer.new",
        "entity_id" to "offer-test",
        "title" to "TEST ONLY - Doorstep alert (no booking)",
        "expires_at" to expiry.toString(),
        "deep_link" to "doorstep-pro://home",
    )

    @Before fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        shadowOf(context.applicationContext as Application).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.DOORSTEP_PRO)
        manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        presenter = NotificationPresenter(context, foreground, alerts)
    }

    @Test fun `foreground offer is posted without requiring an API offer or dialog`() {
        foreground.isForeground = true
        presenter.present(push())
        val posted = shadowOf(manager).allNotifications.single()
        assertThat(posted.channelId).isEqualTo(NotificationChannelSpec.DOORSTEP_PRO_OFFERS.id)
        assertThat(posted.extras.getString(Notification.EXTRA_TITLE)).startsWith("TEST ONLY")
        assertThat(posted.groupAlertBehavior).isEqualTo(Notification.GROUP_ALERT_ALL)
        assertThat(posted.timeoutAfter).isGreaterThan(0L)
        assertThat(posted.timeoutAfter).isAtMost(30_000L)
        assertThat(alerts.claimSound("offer-test", 30_000)).isFalse()
    }

    @Test fun `background offer remains visible on the offers channel`() {
        foreground.isForeground = false
        presenter.present(push())
        assertThat(shadowOf(manager).allNotifications.single().channelId)
            .isEqualTo(NotificationChannelSpec.DOORSTEP_PRO_OFFERS.id)
    }

    @Test fun `card already ringing does not discard push but posts it silently`() {
        foreground.isForeground = true
        assertThat(alerts.claimSound("offer-test", 30_000)).isTrue()
        presenter.present(push())
        val posted = shadowOf(manager).allNotifications.single()
        assertThat(posted.groupAlertBehavior).isEqualTo(Notification.GROUP_ALERT_SUMMARY)
    }

    @Test fun `duplicate push replaces one alert silently`() {
        presenter.present(push())
        presenter.present(push())
        val posted = shadowOf(manager).allNotifications.single()
        assertThat(posted.groupAlertBehavior).isEqualTo(Notification.GROUP_ALERT_SUMMARY)
    }

    @Test fun `expired delivery is not shown`() {
        foreground.isForeground = true
        presenter.present(push(Instant.now().minusSeconds(1)))
        assertThat(shadowOf(manager).allNotifications).isEmpty()
    }

    @Test fun `expiry event clears foreground notification`() {
        foreground.isForeground = true
        presenter.present(push())
        presenter.present(push() + ("type" to "doorstep.pro.offer.expired"))
        assertThat(shadowOf(manager).allNotifications).isEmpty()
    }

    @Test fun `denied notification permission is respected without consuming sound claim`() {
        shadowOf(context.applicationContext as Application).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)
        foreground.isForeground = true
        presenter.present(push())
        assertThat(shadowOf(manager).allNotifications).isEmpty()
        assertThat(alerts.claimSound("offer-test", 30_000)).isTrue()
    }

    @Test fun `all worker review alerts render in foreground and background in unified app`() {
        val unified = NotificationPresenter(context, foreground, alerts,
            java.util.Optional.of(com.us.android.core.notifications.data.PushApp.PARTNER))
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.entries.toSet())
        for (visible in listOf(true, false)) {
            foreground.isForeground = visible
            for (type in listOf("food_delivery_account_updated", "food_delivery_document_reviewed",
                "captain.review.updated", "captain.restricted", "captain.document.expiring",
                "captain.subscription.review_rejected", "doorstep.pro.document.reviewed")) {
                manager.cancelAll()
                unified.present(mapOf("type" to type, "title" to "Review updated", "body" to "Open your application", "entity_id" to "test-review"))
                val posted = shadowOf(manager).allNotifications.single()
                assertThat(posted.channelId).isEqualTo(NotificationChannelSpec.forType(type).id)
                assertThat(posted.extras.getString(Notification.EXTRA_TITLE)).isEqualTo("Review updated")
            }
        }
    }

    @Test fun `four categories have distinct immutable actions and original deadline countdown`() {
        val unified = NotificationPresenter(context, foreground, alerts,
            java.util.Optional.of(com.us.android.core.notifications.data.PushApp.PARTNER))
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.entries.toSet())
        for (category in PartnerOfferCategory.entries) {
            manager.cancelAll()
            val id = java.util.UUID.randomUUID().toString()
            val expiry = Instant.now().plusSeconds(20)
            unified.present(mapOf("type" to category.wireType, "offer_id" to id, "entity_id" to id,
                "title" to "Generic", "expires_at" to expiry.toString(), "test_only" to "true"))
            val posted = shadowOf(manager).allNotifications.single()
            assertThat(posted.extras.getString(Notification.EXTRA_TITLE)).isEqualTo("TEST · ${category.label} · New request")
            assertThat(posted.actions.map { it.title.toString() }).containsExactly("Accept", "Decline").inOrder()
            assertThat(posted.extras.getBoolean(Notification.EXTRA_SHOW_CHRONOMETER)).isTrue()
            assertThat(posted.extras.getBoolean(Notification.EXTRA_CHRONOMETER_COUNT_DOWN)).isTrue()
            assertThat(posted.`when`).isWithin(100L).of(expiry.toEpochMilli())
            assertThat(posted.timeoutAfter).isAtMost(20_000L)
            assertThat(posted.actions[0].actionIntent).isNotEqualTo(posted.actions[1].actionIntent)
            posted.actions.forEach { action ->
                assertThat(action.isAuthenticationRequired).isTrue()
                val target = shadowOf(action.actionIntent).savedIntent
                assertThat(target.component?.className).isEqualTo(PartnerOfferCategory.ACTIVITY)
                assertThat(target.getStringExtra("offer_id")).isEqualTo(id)
                assertThat(target.getStringExtra("type")).isEqualTo(category.wireType)
            }
            // A late delivery clears the original alert instead of granting another twenty seconds.
            unified.present(mapOf("type" to category.wireType, "offer_id" to id, "entity_id" to id,
                "title" to "Generic", "expires_at" to Instant.now().minusSeconds(1).toString()))
            assertThat(shadowOf(manager).allNotifications).isEmpty()
        }
    }

    @Test fun `visible partner panel receives offers instead of tray even when permission denied`() {
        val inbox = PartnerOfferInbox().apply { visibleHost = true }
        val unified = NotificationPresenter(context, foreground, alerts,
            java.util.Optional.of(com.us.android.core.notifications.data.PushApp.PARTNER), inbox)
        foreground.isForeground = true
        shadowOf(context.applicationContext as Application).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)
        repeat(3) { unified.present(push() + ("entity_id" to java.util.UUID.randomUUID().toString())) }
        assertThat(shadowOf(manager).allNotifications).isEmpty()
        assertThat(inbox.offers.value.size).isEqualTo(3)
        // With the panel not visible (background), the same delivery still uses the existing notification path.
        inbox.visibleHost = false
        shadowOf(context.applicationContext as Application).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        unified.present(push())
        assertThat(shadowOf(manager).allNotifications.size).isEqualTo(1)
    }
}
