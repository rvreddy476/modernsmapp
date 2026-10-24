package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** `GET /v1/features` rules (founder decision C, 24 Oct 2026). */
class ProductFeaturesTest {

    @Test
    fun `the product keys are the gateway's, exactly`() {
        // api-gateway cmd/server/features.go featureProductPrefixes.
        assertThat(Product.entries.map { it.wire }).containsExactly(
            "broadcast_channels", "communities", "groups", "qa", "pages",
            "memories", "food", "rider", "dating", "doorstep",
        )
        assertThat(Product.fromWire("dating")).isEqualTo(Product.DATING)
        assertThat(Product.fromWire("shop")).isNull()
    }

    @Test
    fun `with no answer, channels and communities are hidden and the rest follow nothing`() {
        assertThat(productOpen(Product.BROADCAST_CHANNELS, null)).isFalse()
        assertThat(productOpen(Product.COMMUNITIES, null)).isFalse()
        for (p in Product.entries - HIDDEN_WITHOUT_FEATURES) assertThat(productOpen(p, null)).isTrue()
    }

    @Test
    fun `with an answer, only what it named open is open`() {
        val open = setOf(Product.BROADCAST_CHANNELS, Product.FOOD)
        assertThat(productOpen(Product.BROADCAST_CHANNELS, open)).isTrue()
        assertThat(productOpen(Product.FOOD, open)).isTrue()
        assertThat(productOpen(Product.DATING, open)).isFalse()
        assertThat(productOpen(Product.QA, emptySet())).isFalse()
    }

    @Test
    fun `read once per session, then only on a resume ten minutes on, never twice at once`() {
        assertThat(shouldReadFeatures(lastReadAtMs = null, nowMs = 0, inFlight = false)).isTrue()
        assertThat(shouldReadFeatures(lastReadAtMs = null, nowMs = 0, inFlight = true)).isFalse()
        assertThat(shouldReadFeatures(lastReadAtMs = 1_000, nowMs = 1_000 + FEATURES_REFRESH_MS - 1, inFlight = false)).isFalse()
        assertThat(shouldReadFeatures(lastReadAtMs = 1_000, nowMs = 1_000 + FEATURES_REFRESH_MS, inFlight = false)).isTrue()
        assertThat(shouldReadFeatures(lastReadAtMs = 1_000, nowMs = 1_000 + FEATURES_REFRESH_MS, inFlight = true)).isFalse()
        assertThat(FEATURES_REFRESH_MS).isEqualTo(600_000L)
    }

    @Test
    fun `a customer notification lands in its product, Shop in none`() {
        assertThat(productOf(NotificationTarget.Customer(CustomerNotification.Service.HOME, "b1"))).isEqualTo(Product.DOORSTEP)
        assertThat(productOf(NotificationTarget.Customer(CustomerNotification.Service.FOOD, "o1"))).isEqualTo(Product.FOOD)
        assertThat(productOf(NotificationTarget.Customer(CustomerNotification.Service.MOPED, "r1"))).isEqualTo(Product.RIDER)
        assertThat(productOf(NotificationTarget.Customer(CustomerNotification.Service.COMMERCE, "s1"))).isNull()
        assertThat(productOf(NotificationTarget.CustomerDues)).isEqualTo(Product.DOORSTEP)
        assertThat(productOf(NotificationTarget.Post("p1"))).isNull()
    }
}
