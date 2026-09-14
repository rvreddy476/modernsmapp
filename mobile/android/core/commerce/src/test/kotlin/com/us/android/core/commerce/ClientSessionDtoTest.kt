package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.network.CheckoutResultDto
import com.us.android.core.commerce.network.PaymentHandleDto
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * `client_session`, decoded with the app's real Json.
 *
 * 2026-09-14: payments-service adds an OPTIONAL `merchant_display_name` from
 * its per-application registry. The server trims it, caps it at 64 and OMITS
 * the key when empty, so both shapes below are live: a new server with the
 * key, and an older one without it.
 */
class ClientSessionDtoTest {

    private val json = NetworkModule.provideJson()

    private fun handle(clientSession: String): PaymentHandleDto = json.decodeFromString(
        PaymentHandleDto.serializer(),
        """{"payment_intent_id":"pi_1","amount_minor":204000,"currency":"INR","status":"pending",
            "client_session":$clientSession}""",
    )

    @Test
    fun `a client_session with merchant_display_name carries it`() {
        val dto = handle(WITH_NAME)

        assertThat(dto.clientSession).containsExactly(
            "provider", "razorpay",
            "order_id", "order_x",
            "key_id", "rzp_test_x",
            "merchant_display_name", "Momentum Merchant",
        )
    }

    @Test
    fun `a client_session without the key still parses, and has no name`() {
        val dto = handle(WITHOUT_NAME)

        assertThat(dto.clientSession).containsExactly(
            "provider", "razorpay",
            "order_id", "order_x",
            "key_id", "rzp_test_x",
        )
        assertThat(dto.clientSession).doesNotContainKey("merchant_display_name")
    }

    @Test
    fun `checkout's own client_session parses with and without the key`() {
        fun checkout(clientSession: String) = json.decodeFromString(
            CheckoutResultDto.serializer(),
            """{"order_id":"o1","order_number":"MS-1","total_minor":204000,"client_session":$clientSession}""",
        )

        assertThat(checkout(WITH_NAME).clientSession?.get("merchant_display_name")).isEqualTo("Momentum Merchant")
        assertThat(checkout(WITHOUT_NAME).clientSession?.get("merchant_display_name")).isNull()
        assertThat(checkout(WITHOUT_NAME).clientSession?.get("key_id")).isEqualTo("rzp_test_x")
    }

    @Test
    fun `an unknown key beside it is ignored rather than fatal`() {
        val dto = handle(
            """{"provider":"razorpay","order_id":"order_x","key_id":"rzp_test_x",
                "merchant_display_name":"Momentum Merchant","theme":"navy"}""",
        )

        assertThat(dto.clientSession?.get("merchant_display_name")).isEqualTo("Momentum Merchant")
        assertThat(dto.clientSession?.get("key_id")).isEqualTo("rzp_test_x")
    }

    private companion object {
        const val WITH_NAME =
            """{"provider":"razorpay","order_id":"order_x","key_id":"rzp_test_x","merchant_display_name":"Momentum Merchant"}"""
        const val WITHOUT_NAME =
            """{"provider":"razorpay","order_id":"order_x","key_id":"rzp_test_x"}"""
    }
}
