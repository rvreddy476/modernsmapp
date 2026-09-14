package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.model.toRupeeText
import com.us.android.core.food.model.toShortRupeeText
import com.us.android.core.food.network.AddCartItemRequest
import com.us.android.core.food.network.CartAddonRequest
import com.us.android.core.food.network.FeastApi
import com.us.android.core.food.network.PlaceOrderRequest
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.code
import com.us.android.core.food.repository.serverMessage
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

/** The customer routes on the wire: paths, Idempotency-Key headers, bodies, typed refusals. */
class FeastRepositoryTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: FeastRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/").toString())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(FeastApi::class.java)
        repository = FeastRepository(api, json)
    }

    @After
    fun tearDown() = server.close()

    private fun fixture(name: String) = File("src/test/resources/contracts/$name").readText()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).addHeader("Content-Type", "application/json").body(body).build())
    }

    @Test
    fun `placing an order sends the idempotency key and the chosen instrument`() {
        enqueue(fixture("order_get_200.json"), code = 201)

        val result = runBlocking { repository.placeOrder("key-1", PlaceOrderRequest(addressId = "a-1", paymentMethod = "upi")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/food/orders")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("key-1")
        assertThat(request.body?.utf8()).isEqualTo("""{"address_id":"a-1","payment_method":"upi"}""")
        assertThat((result as FoodResult.Success).value.orderNumber).isEqualTo("FG1000000000001")
    }

    @Test
    fun `a payment intent names the order and the method, never an amount`() {
        enqueue(fixture("payment_intent_post_201_client_session.json"), code = 201)

        runBlocking { repository.createPaymentIntent("o-1", "attempt-1", "card") }

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/food/orders/o-1/payments/intents")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("attempt-1")
        assertThat(request.body?.utf8()).isEqualTo("""{"method":"card"}""")
    }

    @Test
    fun `adding to the cart sends add-ons and omits an absent variant`() {
        enqueue(fixture("cart_get_200_section_9_5.json"), code = 201)

        runBlocking {
            repository.addToCart(
                AddCartItemRequest(menuItemId = "m-1", quantity = 2, addons = listOf(CartAddonRequest("ad-1", 1))),
            )
        }

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/food/cart/items")
        assertThat(request.body?.utf8())
            .isEqualTo("""{"menu_item_id":"m-1","quantity":2,"addons":[{"addon_id":"ad-1","quantity":1}]}""")
    }

    @Test
    fun `a pricing refusal keeps the server's code and words`() {
        enqueue(fixture("order_place_422_tax_category_missing.json"), code = 422)

        val result = runBlocking { repository.placeOrder("k", PlaceOrderRequest("a", "upi")) }

        val error = (result as FoodResult.Failure).error
        assertThat(error.code).isEqualTo("FOOD_RESTAURANT_TAX_CATEGORY_MISSING")
        assertThat(error.serverMessage).isEqualTo("restaurant has no GST tax category and cannot take orders")
    }

    @Test
    fun `a payment status of another customer's order is not found`() {
        enqueue(fixture("order_payment_get_404.json"), code = 404)

        val result = runBlocking { repository.orderPayment("o-9") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/food/orders/o-9/payment")
        assertThat((result as FoodResult.Failure).error).isEqualTo(FoodError.NotFound)
    }

    @Test
    fun `rupees are formatted from paise with indian grouping`() {
        assertThat(Paise(64_912).toRupeeText()).isEqualTo("₹649.12")
        assertThat(Paise(1_234_567_89).toRupeeText()).isEqualTo("₹12,34,567.89")
        assertThat(Paise(25_000).toShortRupeeText()).isEqualTo("₹250")
        assertThat(Paise(22_550).toShortRupeeText()).isEqualTo("₹225.50")
        assertThat(Paise(-500).toRupeeText()).isEqualTo("-₹5.00")
    }
}
