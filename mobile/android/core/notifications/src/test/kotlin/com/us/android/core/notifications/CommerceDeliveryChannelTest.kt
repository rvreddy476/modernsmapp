package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class CommerceDeliveryChannelTest {
    @Test fun commerceOffersHaveSeparateAudibleChannelNotAccountOrMerchant() {
        assertThat(NotificationChannelSpec.forType("commerce.delivery.offer")).isEqualTo(NotificationChannelSpec.COMMERCE_DELIVERY_OFFERS)
        assertThat(NotificationChannelSpec.COMMERCE_DELIVERY_OFFERS.alertSound).isTrue()
        assertThat(NotificationChannelSpec.COMMERCE_DELIVERY_OFFERS.id).isEqualTo("commerce_delivery_offers")
        assertThat(NotificationChannelSpec.COMMERCE_DELIVERY).doesNotContain(NotificationChannelSpec.PARTNER_BUSINESS)
        assertThat(NotificationChannelSpec.MOMENTUM).doesNotContain(NotificationChannelSpec.COMMERCE_DELIVERY_OFFERS)
    }
    @Test fun allWorkerEventsRouteToRegisteredCommerceChannel() {
        for (name in listOf("assigned", "picked_up", "delivered", "failed", "cancelled", "earnings", "support", "application.approved", "document.reviewed")) {
            assertThat(NotificationChannelSpec.forType("commerce.delivery.$name")).isEqualTo(NotificationChannelSpec.COMMERCE_DELIVERY_UPDATES)
        }
    }
    @Test fun ongoingLocationHasRegisteredQuietChannel() {
        assertThat(NotificationChannelSpec.COMMERCE_DELIVERY).contains(NotificationChannelSpec.COMMERCE_DELIVERY_DUTY)
        assertThat(NotificationChannelSpec.COMMERCE_DELIVERY_DUTY.alertSound).isFalse()
        assertThat(NotificationChannelSpec.COMMERCE_DELIVERY_DUTY.importance).isEqualTo(android.app.NotificationManager.IMPORTANCE_LOW)
    }
}
