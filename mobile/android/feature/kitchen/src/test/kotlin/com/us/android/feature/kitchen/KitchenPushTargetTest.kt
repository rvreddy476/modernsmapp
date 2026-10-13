package com.us.android.feature.kitchen

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.kitchen.navigation.kitchenPushTarget
import org.junit.Test

class KitchenPushTargetTest {
    private val restaurant = "91c848f6-c57a-459b-bede-6ef154499e87"
    private val order = "4b9cdf76-e64b-44b2-a553-54a42fb2d18d"
    @Test fun orderNavigatesByHintButNeverCarriesState() {
        val target = kitchenPushTarget("food_order_new", "/kitchen/orders/$order?restaurant_id=$restaurant")!!
        assertThat(target.restaurantId).isEqualTo(restaurant)
        assertThat(target.orderId).isEqualTo(order)
        assertThat(target.screen).isEqualTo("order")
    }
    @Test fun applicationAndSettlementAreDistinct() {
        assertThat(kitchenPushTarget("food_kitchen_account_updated", "/kitchen/application?restaurant_id=$restaurant")?.screen).isEqualTo("application")
        assertThat(kitchenPushTarget("food_kitchen_account_updated", "/kitchen/earnings?restaurant_id=$restaurant")?.screen).isEqualTo("earnings")
    }
    @Test fun unrelatedAndMalformedLinksAreRejected() {
        assertThat(kitchenPushTarget("food_delivery_offer", "/kitchen")).isNull()
        for (link in listOf("https://example.com/kitchen", "//evil/kitchen", "/kitchen/orders/not-an-id", "/kitchen?restaurant_id=wrong", "/customer/orders/$order")) {
            assertThat(kitchenPushTarget("food_order_new", link)).isNull()
        }
    }
}
