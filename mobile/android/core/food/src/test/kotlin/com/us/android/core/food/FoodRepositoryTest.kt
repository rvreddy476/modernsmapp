package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.FoodCapabilities
import com.us.android.core.food.network.ComplianceRequest
import com.us.android.core.food.network.FoodApi
import com.us.android.core.food.network.OperatingHoursRequest
import com.us.android.core.food.network.OperatingWindowRequest
import com.us.android.core.food.realtime.FoodRealtimeTokenException
import com.us.android.core.food.realtime.FoodRealtimeTokenSource
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.File

/** The repository on the wire: paths, bodies, and typed failures instead of exceptions. */
class FoodRepositoryTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: FoodRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        repository = repositoryAt(server.url("/").toString())
    }

    @After
    fun tearDown() = server.close()

    private fun repositoryAt(baseUrl: String): FoodRepository {
        val api = Retrofit.Builder()
            .baseUrl(baseUrl)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(FoodApi::class.java)
        return FoodRepository(api, json)
    }

    private fun fixture(name: String) = File("src/test/resources/contracts/$name").readText()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).addHeader("Content-Type", "application/json").body(body).build())
    }

    @Test
    fun `submit before every step is done is a NotReady failure carrying missing`() {
        enqueue(fixture("submit_post_422_not_ready.json"), code = 422)

        val result = runBlocking { repository.submit("r-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/food/partner/restaurants/r-1/submit")
        val error = (result as FoodResult.Failure).error as FoodError.NotReady
        assertThat(error.missing).containsExactly("fssai_document", "payout_account").inOrder()
    }

    @Test
    fun `compliance puts the documented body and omits an absent gstin`() {
        enqueue(fixture("compliance_put_200_eco_without_gstin.json"))

        val result = runBlocking {
            repository.putCompliance(
                "r-1",
                ComplianceRequest(taxCategory = "CLOUD_KITCHEN_TAKEAWAY", legalName = "Test Kitchens LLP", pan = "ZZZPZ0000Z"),
            )
        }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PUT")
        assertThat(request.target).isEqualTo("/v1/food/partner/restaurants/r-1/compliance")
        assertThat(request.body?.utf8())
            .isEqualTo("""{"tax_category":"CLOUD_KITCHEN_TAKEAWAY","legal_name":"Test Kitchens LLP","pan":"ZZZPZ0000Z"}""")
        assertThat((result as FoodResult.Success).value.gstLiability).isEqualTo("ECO_SECTION_9_5")
    }

    @Test
    fun `operating hours send every window field, including false and zero`() {
        enqueue(fixture("operating_hours_put_200.json"))

        runBlocking {
            repository.putOperatingHours(
                "r-1",
                OperatingHoursRequest(listOf(OperatingWindowRequest(dayOfWeek = 0, opensAt = "11:00", closesAt = "15:00", isClosed = false))),
            )
        }

        assertThat(server.takeRequest().body?.utf8())
            .isEqualTo("""{"windows":[{"day_of_week":0,"opens_at":"11:00","closes_at":"15:00","is_closed":false}]}""")
    }

    @Test
    fun `accepting is a PATCH and a not-live restaurant is a typed failure`() {
        enqueue(fixture("accepting_patch_422_not_live.json"), code = 422)

        val result = runBlocking { repository.setAccepting("r-1", accepting = true) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.body?.utf8()).isEqualTo("""{"is_accepting_orders":true}""")
        assertThat(result).isEqualTo(FoodResult.Failure(FoodError.NotLive))
    }

    @Test
    fun `delivery payout account paths and a missing account`() {
        enqueue(fixture("payout_account_get_404.json"), code = 404)

        val result = runBlocking { repository.getDeliveryPayoutAccount() }

        assertThat(server.takeRequest().target).isEqualTo("/v1/food/delivery/payout-account")
        assertThat(result).isEqualTo(FoodResult.Failure(FoodError.NotFound))
    }

    @Test
    fun `capabilities decode from the handler's shape`() {
        enqueue(
            """{"data":{"user_id":"u-1","is_customer":true,"is_restaurant_owner":true,"is_delivery_partner":false,""" +
                """"is_admin":false,"is_moderator":false},"meta":{}}""",
        )

        val result = runBlocking { repository.capabilities() }

        assertThat(server.takeRequest().target).isEqualTo("/v1/food/me/capabilities")
        assertThat(result).isEqualTo(
            FoodResult.Success(FoodCapabilities("u-1", true, true, false, false, false)),
        )
    }

    @Test
    fun `a 404 without a code is a route this server does not have`() {
        enqueue("404 page not found", code = 404)

        assertThat(runBlocking { repository.capabilities() }).isEqualTo(FoodResult.Failure(FoodError.NotAvailable))
    }

    @Test
    fun `a transport failure is a Network failure, not an exception`() {
        val dead = MockWebServer().apply { start() }
        val url = dead.url("/").toString()
        dead.close()

        val result = runBlocking { repositoryAt(url).capabilities() }

        assertThat((result as FoodResult.Failure).error).isInstanceOf(FoodError.Network::class.java)
    }

    @Test
    fun `the realtime token is cached until a refresh is forced`() {
        enqueue("""{"data":{"token":"tok-1","topics":["food.order.1"]}}""")
        enqueue("""{"data":{"token":"tok-2","topics":["food.order.1"]}}""")
        val source = FoodRealtimeTokenSource(repository)

        val tokens = runBlocking { listOf(source.token(false), source.token(false), source.token(true)) }

        assertThat(tokens).containsExactly("tok-1", "tok-1", "tok-2").inOrder()
        assertThat(server.requestCount).isEqualTo(2)
        val first = server.takeRequest()
        assertThat(first.method).isEqualTo("POST")
        assertThat(first.target).isEqualTo("/v1/food/realtime/token")
    }

    @Test
    fun `a token issuer failure throws for the SSE client to back off on`() {
        enqueue("""{"error":{"code":"REALTIME_TOKEN_FAILED","message":"realtime: signer not configured"}}""", code = 500)

        val thrown = runCatching { runBlocking { FoodRealtimeTokenSource(repository).token(false) } }.exceptionOrNull()

        assertThat(thrown).isInstanceOf(FoodRealtimeTokenException::class.java)
    }
}
