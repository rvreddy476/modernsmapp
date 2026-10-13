package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.PartnerWorkReadbackDto
import com.us.android.core.network.PartnerWorkSessionRevisionDto
import com.us.android.feature.mopedu.captain.data.*
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLink
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinks
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import retrofit2.Response

class CaptainRideReadbackTest {
    private val json = Json { ignoreUnknownKeys = true }
    private val ride = json.decodeFromString<CaptainRideDto>("""{
      "id":"ride-1","revision":12,"status":"arrived","vehicle_type":"bike",
      "pickup_address":"Pickup","pickup_lat":17.4,"pickup_lng":78.4,
      "drop_address":"Drop","drop_lat":17.5,"drop_lng":78.5,
      "estimated_fare":95.50,"final_fare":110.0,"final_fare_paise":11075,
      "requested_at":"2026-10-11T04:00:00Z","otp":"must-not-be-used"
    }""")

    @Test fun `duty changes carry server work revision and never invent it`() = runTest {
        val api = mockk<MopeduCaptainApi>()
        coEvery { api.workSession() } returns Response.success(ApiEnvelope(data = PartnerWorkReadbackDto(PartnerWorkSessionRevisionDto(31, "moped"))))
        coEvery { api.goOnline(31) } returns Response.success(ApiEnvelope(data = Unit))
        coEvery { api.goOffline(31) } returns Response.success(ApiEnvelope(data = Unit))
        val repo = RealMopeduCaptainRepository(api, json)
        assertThat(repo.setOnline(true)).isInstanceOf(CaptainResult.Success::class.java)
        assertThat(repo.setOnline(false)).isInstanceOf(CaptainResult.Success::class.java)
        coVerify(exactly = 2) { api.workSession() }
        coVerify(exactly = 1) { api.goOnline(31) }
        coVerify(exactly = 1) { api.goOffline(31) }
    }

    @Test fun `ride readback uses server revision and exact final paise without OTP`() {
        val booking = ride.toBooking()
        assertThat(booking.revision).isEqualTo(12)
        assertThat(booking.estimatedFare.paise).isEqualTo(9550)
        assertThat(booking.finalFare?.paise).isEqualTo(11075)
        assertThat(booking.otp).isNull()
    }

    @Test fun `mutations read server revision and never send an empty body`() = runTest {
        val api = mockk<MopeduCaptainApi>()
        coEvery { api.ride("ride-1") } returns Response.success(ApiEnvelope(data = ride))
        coEvery { api.markArrived("ride-1", RideRevisionDto(12)) } returns Response.success(ApiEnvelope(data = Unit))
        coEvery { api.startRide("ride-1", StartRideRequestDto("1234", 12)) } returns Response.success(ApiEnvelope(data = Unit))
        coEvery { api.completeRide("ride-1", CompleteRideRequestDto(5.0, 20, "key", 12)) } returns Response.success(ApiEnvelope(data = Unit))
        coEvery { api.confirmCashPayment("ride-1", RideRevisionDto(12)) } returns Response.success(ApiEnvelope(data = Unit))
        val repo = RealMopeduCaptainRepository(api, json)
        assertThat(repo.markArrived("ride-1")).isInstanceOf(CaptainResult.Success::class.java)
        assertThat(repo.verifyOtpAndStart("ride-1", "1234")).isInstanceOf(CaptainResult.Success::class.java)
        assertThat(repo.completeRide("ride-1", 5.0, 20, "key")).isInstanceOf(CaptainResult.Success::class.java)
        assertThat(repo.confirmCashPayment("ride-1")).isInstanceOf(CaptainResult.Success::class.java)
        coVerify(exactly = 4) { api.ride("ride-1") }
    }

    @Test fun `malformed expiry never gives a renewed offer window`() {
        for (expiry in listOf(null, "invalid")) {
            val offer = CaptainOfferDto("offer", "ride", expiresAt = expiry).toOffer()
            assertThat(offer.isExpiredAt(1)).isTrue()
        }
    }

    @Test fun `ride notification identifies the job but offer always rereads requests`() {
        val id = "33333333-3333-4333-8333-333333333333"
        assertThat(CaptainDeepLinks.fromPush("captain.ride.cancelled", id)).isEqualTo(CaptainDeepLink.Ride(id))
        assertThat(CaptainDeepLinks.fromPush("captain.offer", id)).isEqualTo(CaptainDeepLink.Work)
    }
}
