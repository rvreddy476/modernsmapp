package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.data.PushApp
import org.junit.Test

class PartnerNotificationScopeTest {
    @Test fun workerAppAcceptsOnlyItsJobsAndAccountNotices() {
        for (type in listOf("doorstep.pro.offer.new", "doorstep.pro.application.approved", "food_delivery_offer", "captain.offer")) {
            assertThat(acceptsInstalledAppNotification(PushApp.PARTNER, type)).isTrue()
        }
    }
    @Test fun merchantsAndCustomersDoNotRingWorkerApp() {
        for (type in listOf(null, "food_order_new", "food.restaurant.approved", "commerce.seller.new_order", "seller.created", "food_order_status", "comment")) {
            assertThat(acceptsInstalledAppNotification(PushApp.PARTNER, type)).isFalse()
            assertThat(acceptsInstalledAppNotification(PushApp.MOMENTUM, type)).isTrue()
        }
    }
    @Test fun kitchenAcceptsOnlyMerchantMessages() {
        listOf("food_order_new", "food_kitchen_account_updated", "food_kitchen_order_updated", "food.restaurant.approved").forEach {
            assertThat(acceptsInstalledAppNotification(PushApp.FEAST_KITCHEN, it)).isTrue()
        }
        listOf(null, "food_order_status", "food_delivery_offer", "commerce.seller.new_order", "comment").forEach {
            assertThat(acceptsInstalledAppNotification(PushApp.FEAST_KITCHEN, it)).isFalse()
        }
    }
}
