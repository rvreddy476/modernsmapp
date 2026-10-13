package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

class PartnerBusinessWireTest {
    private lateinit var server: MockWebServer
    private lateinit var api: PartnerWorkspaceApi

    @Before fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder().baseUrl(server.url("/"))
            .addConverterFactory(NetworkModule.provideJson().asConverterFactory("application/json".toMediaType()))
            .build().create(PartnerWorkspaceApi::class.java)
    }

    @After fun tearDown() = server.close()

    private fun enqueue(status: Int, body: String) {
        server.enqueue(MockResponse.Builder().code(status).addHeader("Content-Type", "application/json").body(body).build())
    }

    @Test fun actualClosedGatewayResponseIsUnavailableNotAnApprovalError() = runBlocking {
        // Exact public gateway response observed on 2026-10-10 while RIDER_PUBLIC_ENABLED=false.
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"Not found"}}""")
        val result = loadBusinessSummary(api, PartnerBusiness.MOPED)
        assertThat(result.status).isEqualTo(BusinessStatus.UNAVAILABLE)
        assertThat(result.registered).isFalse()
        assertThat(result.canOpen).isFalse()
        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/rider/partners/me")
        assertThat(request.headers["Cache-Control"]).isEqualTo("no-cache")
    }

    @Test fun profileNotFoundIsDifferentFromClosedProductAndAllowsOnboarding() = runBlocking {
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"no partner profile"}}""")
        val result = loadBusinessSummary(api, PartnerBusiness.MOPED)
        assertThat(result.status).isEqualTo(BusinessStatus.NEW)
        assertThat(result.canOpen).isTrue()
    }

    @Test fun enabledApprovedMopedStillUsesActualServerApproval() = runBlocking {
        enqueue(200, """{"data":{"id":"test-profile","full_name":"Test Partner","status":"approved"}}""")
        val result = loadBusinessSummary(api, PartnerBusiness.MOPED)
        assertThat(result.status).isEqualTo(BusinessStatus.APPROVED)
        assertThat(result.name).isEqualTo("Test Partner")
        assertThat(result.registered).isTrue()
        assertThat(result.canOpen).isTrue()
    }

    @Test fun realAuthServerAndMalformedErrorsAreNotSilencedAsUnavailableOrNew() = runBlocking {
        for ((status, body) in listOf(
            401 to """{"error":{"code":"UNAUTHORIZED"}}""",
            403 to """{"error":{"code":"FORBIDDEN"}}""",
            500 to """{"error":{"code":"PARTNER_FETCH_FAILED"}}""",
            404 to "<html>Not found</html>",
            200 to """{"data":null}""",
        )) {
            enqueue(status, body)
            val result = loadBusinessSummary(api, PartnerBusiness.MOPED)
            assertThat(result.status).isEqualTo(BusinessStatus.UNKNOWN)
            assertThat(result.registered).isFalse()
        }
    }

    @Test fun commerceReadsItsOwnWorkerProfileNeverTheSellerProfile() = runBlocking {
        enqueue(404, """{"error":{"code":"COMMERCE_DELIVERY_PROFILE_NOT_FOUND"}}""")
        assertThat(loadBusinessSummary(api, PartnerBusiness.COMMERCE_DELIVERY).status).isEqualTo(BusinessStatus.NEW)
        assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/delivery/profile")
    }

    @Test(expected = CancellationException::class)
    fun cancellingARefreshDoesNotProduceABusinessFailure() = runBlocking {
        val cancelled = object : PartnerWorkspaceApi {
            override suspend fun profile(path: String): Response<JsonObject> = throw CancellationException("test")
        }
        loadBusinessSummary(cancelled, PartnerBusiness.MOPED)
        Unit
    }
}
