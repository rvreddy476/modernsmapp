package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.outcomeUnknown
import org.junit.Test
import java.io.IOException

/**
 * Which failures leave a request's outcome unknown, so its Idempotency-Key must
 * be kept and resent (food audit M1). Freeing the key on one of these is how an
 * order is placed twice in the customer's mind and orphaned on the server.
 */
class FoodOutcomeUnknownTest {

    @Test
    fun `a dropped connection, a gateway error and an unreadable success are unknown`() {
        assertThat(FoodError.Network(IOException("reset")).outcomeUnknown).isTrue()
        assertThat(FoodError.Unexpected(502, null, null).outcomeUnknown).isTrue()
        assertThat(FoodError.Unexpected(504, null, null).outcomeUnknown).isTrue()
        assertThat(FoodError.Unexpected(500, "INTERNAL", "boom").outcomeUnknown).isTrue()
        assertThat(FoodError.Unexpected(null, "MALFORMED_RESPONSE", null).outcomeUnknown).isTrue()
        assertThat(FoodError.Unexpected(201, null, "2xx response carried no data").outcomeUnknown).isTrue()
    }

    @Test
    fun `the server still running the first request under the key is unknown`() {
        assertThat(FoodError.Unexpected(409, "FOOD_IDEMPOTENCY_IN_PROGRESS", "in progress").outcomeUnknown).isTrue()
    }

    @Test
    fun `a definite refusal is known, so the key may be freed`() {
        assertThat(FoodError.Unexpected(400, "FOOD_ORDER_PLACE_FAILED", "cart is empty").outcomeUnknown).isFalse()
        assertThat(FoodError.Unexpected(409, "FOOD_IDEMPOTENCY_PAYLOAD_MISMATCH", null).outcomeUnknown).isFalse()
        assertThat(FoodError.Unexpected(499, null, null).outcomeUnknown).isFalse()
        assertThat(FoodError.InvalidField("FOOD_PRICING_CHANGED", null, "prices changed").outcomeUnknown).isFalse()
        assertThat(FoodError.NotFound.outcomeUnknown).isFalse()
        assertThat(FoodError.Unauthorized.outcomeUnknown).isFalse()
        assertThat(FoodError.InvalidBody.outcomeUnknown).isFalse()
    }
}
