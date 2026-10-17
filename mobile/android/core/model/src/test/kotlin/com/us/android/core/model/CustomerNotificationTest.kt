package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class CustomerNotificationTest {
    @Test fun customerLinksResolveFromInboxAndPush() {
        val rows = listOf(
            Triple("doorstep.booking.assigned", "/doorstep/bookings/job-1", CustomerNotification.Service.HOME),
            Triple("food_order_status", "/feast/orders/order-1", CustomerNotification.Service.FOOD),
            Triple("order_shipped", "/shop/orders/order-1", CustomerNotification.Service.COMMERCE),
            Triple("ride.completed", "/mopedu/booking/ride-1", CustomerNotification.Service.MOPED),
        )
        rows.forEach { (type, link, service) ->
            val target = NotificationTarget.Customer(service, link.substringAfterLast('/'))
            assertThat(NotificationTarget.parse(link)).isEqualTo(target)
            assertThat(CustomerNotification.resolve(type, "other", link)).isEqualTo(target)
            assertThat(CustomerNotification.resolve(type, target.id, "")).isEqualTo(target)
            assertThat(CustomerNotification.description(type)).isNotNull()
        }
    }
    @Test fun refusesExternalAndMerchantLinks() {
        listOf("https://evil.test/shop/orders/one", "//evil.test/shop/orders/one", "/shop/orders/../bad", "/shop/orders/a%2fb", "/shop/sell/orders/one", "/shop/orders/one/unexpected").forEach {
            assertThat(CustomerNotification.resolve("order_shipped", "one", it)).isEqualTo(NotificationTarget.None)
        }
        assertThat(CustomerNotification.resolve("seller_new_order", "one", "/shop/orders/one")).isEqualTo(NotificationTarget.None)
    }
    @Test fun invoiceAndFailedPaymentHaveTheirOwnWords() {
        assertThat(CustomerNotification.description("commerce_invoice_issued")).isEqualTo("Shopping order: invoice ready")
        assertThat(CustomerNotification.description("order_payment_failed")).isEqualTo("Shopping order: payment failed")
        assertThat(CustomerNotification.resolve("commerce_invoice_issued", "", "/shop/orders/order-1"))
            .isEqualTo(NotificationTarget.Customer(CustomerNotification.Service.COMMERCE, "order-1"))
    }
    @Test fun mobileSchemeAndDuesSupported() {
        assertThat(NotificationTarget.parse("momentum://doorstep/bookings/one/chat")).isEqualTo(NotificationTarget.Customer(CustomerNotification.Service.HOME,"one"))
        assertThat(CustomerNotification.resolve("doorstep.outstanding.due", "", "")).isEqualTo(NotificationTarget.CustomerDues)
    }
}
