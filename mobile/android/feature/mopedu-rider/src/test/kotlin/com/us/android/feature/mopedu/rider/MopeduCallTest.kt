package com.us.android.feature.mopedu.rider

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.mopedu.rider.data.MopeduError
import com.us.android.feature.mopedu.rider.data.MopeduResult
import com.us.android.feature.mopedu.rider.data.RidePaymentDto
import com.us.android.feature.mopedu.rider.data.code
import com.us.android.feature.mopedu.rider.data.mopeduCall
import com.us.android.feature.mopedu.rider.data.mopeduUnitCall
import com.us.android.feature.mopedu.rider.data.toEpochMs
import com.us.android.feature.mopedu.rider.data.userMessage
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.Response
import java.io.IOException

class MopeduCallTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `a 422 with the platform error envelope is a refusal with its stable code`() {
        val error = MopeduError.from(422, """{"error":{"code":"COUPON_INVALID","message":"That code isn't valid."},"meta":{"request_id":"r"}}""", json)
        assertThat(error).isEqualTo(MopeduError.Refused(422, "COUPON_INVALID", "That code isn't valid."))
        assertThat(error.code).isEqualTo("COUPON_INVALID")
        assertThat(error.userMessage()).isEqualTo("That code isn't valid.")
    }

    @Test
    fun `401 is unauthorized, a bare 404 is not found, and anything else is unexpected`() {
        assertThat(MopeduError.from(401, null, json)).isEqualTo(MopeduError.Unauthorized)
        assertThat(MopeduError.from(404, "not found", json)).isEqualTo(MopeduError.NotFound)
        assertThat(MopeduError.from(500, "<html>boom</html>", json)).isEqualTo(MopeduError.Unexpected(500, "<html>boom</html>"))
        assertThat(MopeduError.from(404, """{"error":{"code":"RIDE_NOT_FOUND","message":"No such ride"}}""", json))
            .isEqualTo(MopeduError.Refused(404, "RIDE_NOT_FOUND", "No such ride"))
    }

    @Test
    fun `a successful call carries the data, and an additive field does not break it`() = runTest {
        val body = """{"data":{"method":"upi","status":"paid","amount_paise":6500,"refunded_paise":0,"settled_at":"2026-09-18T10:00:00Z"}}"""
        val decoded = json.decodeFromString(ApiEnvelope.serializer(RidePaymentDto.serializer()), body)
        val result = mopeduCall(json) { Response.success(decoded) }
        assertThat(result).isEqualTo(MopeduResult.Success(RidePaymentDto(method = "upi", status = "paid", amountPaise = 6500)))
    }

    @Test
    fun `an error response becomes a typed failure and IO becomes a network failure`() = runTest {
        val refused = mopeduCall<RidePaymentDto>(json) {
            Response.error(409, """{"error":{"code":"RIDE_STATE","message":"Already paid"}}""".toResponseBody("application/json".toMediaType()))
        }
        assertThat(refused).isEqualTo(MopeduResult.Failure(MopeduError.Refused(409, "RIDE_STATE", "Already paid")))

        val network = mopeduCall<RidePaymentDto>(json) { throw IOException("offline") }
        assertThat((network as MopeduResult.Failure).error).isInstanceOf(MopeduError.Network::class.java)

        val unit = mopeduUnitCall(json) { Response.success(ApiEnvelope<Unit>()) }
        assertThat(unit).isEqualTo(MopeduResult.Success(Unit))
    }

    @Test
    fun `a 2xx with null data is unexpected unless an empty value is allowed`() = runTest {
        val noData = mopeduCall<List<String>>(json) { Response.success(ApiEnvelope()) }
        assertThat((noData as MopeduResult.Failure).error).isInstanceOf(MopeduError.Unexpected::class.java)

        val empty = mopeduCall(json, empty = emptyList<String>()) { Response.success(ApiEnvelope()) }
        assertThat(empty).isEqualTo(MopeduResult.Success(emptyList<String>()))
    }

    @Test
    fun `ISO instants parse with Z or an offset, and garbage is null`() {
        assertThat("2026-09-18T10:00:00Z".toEpochMs()).isEqualTo(1_789_725_600_000L)
        assertThat("2026-09-18T15:30:00+05:30".toEpochMs()).isEqualTo(1_789_725_600_000L)
        assertThat("soon".toEpochMs()).isNull()
        assertThat(null.toEpochMs()).isNull()
        assertThat("".toEpochMs()).isNull()
    }
}
