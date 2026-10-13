package com.us.android.core.notifications

import org.junit.Assert.assertEquals
import org.junit.Test

class DoorstepNotificationDataTest {
    @Test fun `foreground notification envelope supplies missing partner title and body`() {
        val data = mapOf("type" to "doorstep.pro.offer.new", "entity_id" to "offer")
        assertEquals(data + mapOf("title" to "New request", "body" to "Open Doorstep"),
            doorstepNotificationData(data, "New request", "Open Doorstep"))
    }

    @Test fun `data-only payload retains its authoritative text`() {
        val data = mapOf("type" to "doorstep.pro.offer.new", "title" to "Data title", "body" to "Data body")
        assertEquals(data, doorstepNotificationData(data, "Envelope title", "Envelope body"))
        assertEquals(data, doorstepNotificationData(data, null, null))
    }

    @Test fun `other app and chat payloads never inherit notification text`() {
        for (type in listOf("dm", "message_request", "captain.offer", "food_order_new")) {
            val data = mapOf("type" to type)
            assertEquals(data, doorstepNotificationData(data, "Do not inherit", "Do not inherit"))
        }
    }
}
