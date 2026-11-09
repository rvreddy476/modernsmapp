package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.KitchenApi
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.core.food.repository.PartnerOrders
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

/**
 * The Kitchen's active orders, read from the status-filtered, cursor-paged
 * partner orders list (food audit M5, food server lane 9 Nov 2026).
 */
class KitchenOrdersPagingTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: KitchenRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/").toString())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(KitchenApi::class.java)
        repository = KitchenRepository(api, json)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(
            MockResponse.Builder()
                .code(code)
                .addHeader("Content-Type", "application/json")
                .body(body)
                .build(),
        )
    }

    private fun page(id: String, status: String, next: String?): String {
        val meta = next?.let { ""","meta":{"next_cursor":"$it"}""" }.orEmpty()
        return """{"data":{"items":[{"id":"$id","status":"$status"}]}$meta}"""
    }

    private val active = listOf("PREPARING", "READY_FOR_PICKUP")

    @Test
    fun `the first page names the statuses and the page size, and carries no cursor`() {
        enqueue(File("src/test/resources/contracts/partner_orders_get_200_page.json").readText())
        enqueue(page("o-3", "PREPARING", next = null))

        runBlocking { repository.ordersIn("r-1", active) }

        val first = server.takeRequest().url
        assertThat(first.encodedPath).isEqualTo("/v1/food/partner/restaurants/r-1/orders")
        assertThat(first.queryParameterValues("status")).containsExactly("PREPARING", "READY_FOR_PICKUP").inOrder()
        assertThat(first.queryParameter("limit")).isEqualTo("100")
        assertThat(first.queryParameter("cursor")).isNull()
    }

    @Test
    fun `each next page sends back the cursor untouched, and the read stops when none is given`() {
        enqueue(File("src/test/resources/contracts/partner_orders_get_200_page.json").readText())
        enqueue(page("o-3", "READY_FOR_PICKUP", next = "opaque/+cursor=2"))
        enqueue(page("o-4", "PREPARING", next = null))

        val result = runBlocking { repository.ordersIn("r-1", active) }

        val read = (result as FoodResult.Success).value
        assertThat(read.complete).isTrue()
        assertThat(read.items.map { it.id }).containsExactly(
            "0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015",
            "0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0032",
            "o-3",
            "o-4",
        ).inOrder()
        assertThat(server.requestCount).isEqualTo(3)
        server.takeRequest()
        assertThat(server.takeRequest().url.queryParameter("cursor"))
            .isEqualTo("1789280940000000.0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0032")
        assertThat(server.takeRequest().url.queryParameter("cursor")).isEqualTo("opaque/+cursor=2")
    }

    @Test
    fun `a server that hands back a cursor it already gave stops the read as incomplete`() {
        enqueue(page("o-1", "PREPARING", next = "c-1"))
        enqueue(page("o-2", "PREPARING", next = "c-2"))
        enqueue(page("o-3", "PREPARING", next = "c-1"))
        enqueue(page("o-4", "PREPARING", next = null))

        val result = runBlocking { repository.ordersIn("r-1", active) }

        assertThat(result).isEqualTo(FoodResult.Success(PartnerOrders(itemsOf(result), complete = false)))
        assertThat(itemsOf(result)).hasSize(3)
        assertThat(server.requestCount).isEqualTo(3)
    }

    @Test
    fun `the read stops after the page cap, as incomplete`() {
        repeat(3) { enqueue(page("o-$it", "PREPARING", next = "c-$it")) }
        enqueue(page("o-last", "PREPARING", next = null))

        val result = runBlocking { repository.ordersIn("r-1", active, maxPages = 3) }

        assertThat((result as FoodResult.Success).value.complete).isFalse()
        assertThat(result.value.items).hasSize(3)
        assertThat(server.requestCount).isEqualTo(3)
    }

    @Test
    fun `a page that fails fails the whole read, never a partial list shown as the full one`() {
        enqueue(page("o-1", "PREPARING", next = "c-1"))
        enqueue("""{"error":{"code":"INVALID_CURSOR","message":"cursor is not one this list returned"}}""", code = 400)

        val result = runBlocking { repository.ordersIn("r-1", active) }

        assertThat(result).isInstanceOf(FoodResult.Failure::class.java)
    }

    @Test
    fun `no statuses asks for every status, with no status parameter`() {
        enqueue(page("o-1", "DELIVERED", next = null))

        runBlocking { repository.ordersIn("r-1", emptyList()) }

        assertThat(server.takeRequest().url.queryParameterValues("status")).isEmpty()
    }

    private fun itemsOf(result: FoodResult<PartnerOrders>) = (result as FoodResult.Success).value.items
}
