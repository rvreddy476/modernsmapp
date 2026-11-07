package com.us.android.core.notifications

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.Callable
import java.util.concurrent.Executors

class DoorstepOfferAlertCoordinatorTest {
    private val alerts = DoorstepOfferAlertCoordinator()

    @Test fun `offer pushes stay visible with app open or in background`() {
        assertFalse(suppressForegroundNotification("doorstep.pro.offer.new", true, chatSocketLive = true))
        assertFalse(suppressForegroundNotification("doorstep.pro.offer.new", false, chatSocketLive = true))
        assertFalse(suppressForegroundNotification("doorstep.pro.application.approved", true, chatSocketLive = true))
    }

    @Test fun `a foreground chat push is dropped only while the chat socket is live`() {
        assertTrue(suppressForegroundNotification("dm", isForeground = true, chatSocketLive = true))
        assertTrue(suppressForegroundNotification("message_request", isForeground = true, chatSocketLive = true))
        assertFalse(suppressForegroundNotification("dm", isForeground = false, chatSocketLive = true))
    }

    @Test fun `a foreground chat push shows while the chat socket is down`() {
        // Audit H1: a cold start before the first token refresh left the
        // socket dead and every foreground chat push dropped.
        assertFalse(suppressForegroundNotification("dm", isForeground = true, chatSocketLive = false))
        assertFalse(suppressForegroundNotification("message_request", isForeground = true, chatSocketLive = false))
    }

    @Test fun `standalone foreground push can sound without a server offer card`() {
        assertTrue(alerts.claimSound("test-alert", 30_000, 0))
    }

    @Test fun `notification first prevents a competing foreground ringtone`() {
        assertTrue(alerts.claimSound("offer", 30_000, 0))
        assertFalse(alerts.claimSound("offer", 29_000, 1_000))
    }

    @Test fun `foreground card first makes the subsequent notification silent`() {
        assertTrue(alerts.claimSound("offer", 30_000, 0))
        assertFalse(alerts.claimSound("offer", 28_000, 2_000))
        assertFalse(alerts.claimSound("offer", 27_000, 3_000))
        assertTrue(alerts.claimSound("different-offer", 30_000, 3_000))
    }

    @Test fun `invalid offers do not claim sound and expired claims are discarded`() {
        assertFalse(alerts.claimSound("", 30_000, 0))
        assertFalse(alerts.claimSound("offer", 0, 0))
        assertTrue(alerts.claimSound("offer", 30_000, 0))
        assertTrue(alerts.claimSound("offer", 30_000, 30_000))
    }

    @Test fun `simultaneous push and UI arrivals have only one sound owner`() {
        val threads = Executors.newFixedThreadPool(2)
        try {
            val claims = threads.invokeAll(List(20) { Callable { alerts.claimSound("offer", 30_000, 0) } })
            org.junit.Assert.assertEquals(1, claims.count { it.get() })
        } finally {
            threads.shutdownNow()
        }
    }
}
