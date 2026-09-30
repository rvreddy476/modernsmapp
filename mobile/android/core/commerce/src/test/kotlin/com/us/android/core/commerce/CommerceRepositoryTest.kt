package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.NewProduct
import com.us.android.core.commerce.model.OrderPaymentState
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.commerce.repository.isPermanentRefusal
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.util.concurrent.TimeUnit

/**
 * The repository over a real wire: what it sends for the seller's variant
 * edits, how it pages, and what it makes of the server's answers.
 *
 * The first group protects the 2026-09-30 variant-id fix. `SellerScreen`
 * used to open the stock editor with the PRODUCT id as the variant id; the
 * server looks up `product_variants.id`, so every stock and price edit
 * answered variant-not-found. The guard is that the id reaching
 * `/seller/variants/{id}/stock` is one the server named as a variant, and
 * these tests fail if a product id is ever sent there.
 */
class CommerceRepositoryTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repo: CommerceRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CommerceApi::class.java)
        repo = CommerceRepository(api)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun bareProduct(id: String = "p-1") = SellerProduct(
        id = id,
        title = "Kettle",
        status = "active",
        approvalStatus = "approved",
        rejectionReason = null,
        imageUrl = null,
    )

    // ─── The variant-id guard ────────────────────────────────────────

    @Test
    fun `a seller row without variants is resolved through the product's variants route, never the product id`() =
        runBlocking {
            enqueue("""{"data":{"items":[{"id":"v-1","sku":"SKU-1","available_qty":4}]}}""")
            enqueue("""{"data":{"variant_id":"v-1","total_qty":4,"reserved_qty":0,"available":4}}""")

            val variants = (repo.variantsForEdit(bareProduct("p-1")) as CommerceResult.Success).value
            val variantId = variants.single().id
            repo.stock(variantId)

            assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/products/p-1/variants")
            // The whole point: the stock path carries the VARIANT's id.
            assertThat(variantId).isEqualTo("v-1")
            assertThat(variantId).isNotEqualTo("p-1")
            assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/seller/variants/v-1/stock")
        }

    @Test
    fun `a seller row that carries its variants needs no second read`() = runBlocking {
        enqueue(
            """{"data":{"items":[{"id":"p-1","title":"Kettle","status":"active","approval_status":"approved",""" +
                """"variants":[{"id":"v-1","sku":"SKU-1","selling_price_minor":199900,""" +
                """"available_qty":4}]}],"total":1}}""",
        )

        val product = (repo.sellerProducts() as CommerceResult.Success).value.single()
        val variants = (repo.variantsForEdit(product) as CommerceResult.Success).value

        assertThat(product.variants.map { it.id }).containsExactly("v-1")
        assertThat(variants.single().id).isEqualTo("v-1")
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `three variants on the server are three choices, none of them the product`() = runBlocking {
        enqueue(
            """{"data":{"items":[{"id":"v-1","sku":"S","option_1_name":"Size","option_1_value":"S"},""" +
                """{"id":"v-2","sku":"M","option_1_name":"Size","option_1_value":"M"},""" +
                """{"id":"v-3","sku":"L","option_1_name":"Size","option_1_value":"L"}]}}""",
        )

        val variants = (repo.variantsForEdit(bareProduct("p-1")) as CommerceResult.Success).value

        assertThat(variants.map { it.id }).containsExactly("v-1", "v-2", "v-3").inOrder()
        assertThat(variants.map { it.id }).doesNotContain("p-1")
        assertThat(variants.first().options.single().value).isEqualTo("S")
    }

    @Test
    fun `a product with no variants anywhere is an empty answer, not a guess`() = runBlocking {
        enqueue("""{"data":{"items":[]}}""")

        val variants = (repo.variantsForEdit(bareProduct("p-1")) as CommerceResult.Success).value

        assertThat(variants).isEmpty()
    }

    @Test
    fun `the create response's variant is used when the server sends one`() = runBlocking {
        enqueue(
            """{"data":{"id":"p-9","title":"Kettle","status":"draft","approval_status":"draft",""" +
                """"variants":[{"id":"v-9","sku":"SKU-9"}]}}""",
            code = 201,
        )

        val created = (repo.createProduct(newProduct()) as CommerceResult.Success).value

        assertThat(created.id).isEqualTo("p-9")
        assertThat(created.variants.single().id).isEqualTo("v-9")
        assertThat(server.takeRequest().body!!.utf8()).contains(""""category_id":"cat-1"""")
    }

    @Test
    fun `the create response's default_variant_id is the last resort on a server without the variants route`() =
        runBlocking {
            enqueue("""{"data":{"id":"p-9","title":"Kettle","default_variant_id":"v-9"}}""", code = 201)
            // An older server: no public variants route.
            enqueue("""{"error":{}}""", code = 404)

            val created = (repo.createProduct(newProduct()) as CommerceResult.Success).value
            val variants = (repo.variantsForEdit(created) as CommerceResult.Success).value

            assertThat(variants.single().id).isEqualTo("v-9")
            server.takeRequest()
            assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/products/p-9/variants")
        }

    private fun newProduct() = NewProduct(
        title = "Kettle",
        description = null,
        taxClassId = "tax-18",
        sku = "SKU-9",
        mrp = Paise(100),
        sellingPrice = Paise(100),
        openingStock = 1,
        categoryId = "cat-1",
    )

    // ─── Paging ──────────────────────────────────────────────────────

    @Test
    fun `the order list keeps the server's cursor and asks for the next page with it`() = runBlocking {
        enqueue("""{"data":{"items":[${order("o-1")}],"next_cursor":"page-2"}}""")
        enqueue("""{"data":{"items":[${order("o-2")}],"next_cursor":""}}""")

        val first = (repo.orders() as CommerceResult.Success).value
        val second = (repo.orders(first.nextCursor) as CommerceResult.Success).value

        assertThat(first.items.map { it.id }).containsExactly("o-1")
        assertThat(first.nextCursor).isEqualTo("page-2")
        assertThat(second.items.map { it.id }).containsExactly("o-2")
        // Go's zero value: an empty cursor is no cursor.
        assertThat(second.nextCursor).isNull()
        assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/orders?limit=20")
        assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/orders?cursor=page-2&limit=20")
    }

    private fun order(id: String) =
        """{"id":"$id","order_number":"MS-$id","status":"confirmed","payment_status":"paid","total_minor":100}"""

    // ─── Unknown-shape rows ──────────────────────────────────────────

    @Test
    fun `a catalogue row without an id is skipped, not a crash`(): Unit = runBlocking {
        enqueue("""{"data":{"items":[{"title":"No id"},{"id":"p-2","title":"Kettle"}]}}""")

        val page = (repo.products() as CommerceResult.Success).value

        assertThat(page.items.map { it.id }).containsExactly("p-2")
    }

    @Test
    fun `an order row without an id is skipped, and an address without one too`(): Unit = runBlocking {
        enqueue("""{"data":{"items":[{"order_number":"x"},${order("o-2")}]}}""")
        enqueue("""{"data":[{"contact_name":"nobody"},{"id":"a-1","contact_name":"Asha"}]}""")

        val orders = (repo.orders() as CommerceResult.Success).value
        val addresses = (repo.addresses() as CommerceResult.Success).value

        assertThat(orders.items.map { it.id }).containsExactly("o-2")
        assertThat(addresses.map { it.id }).containsExactly("a-1")
    }

    @Test
    fun `an order detail without its address still decodes`() = runBlocking {
        enqueue("""{"data":{"id":"o-1","order_number":"MS-1","status":"payment_failed","can_retry_payment":true}}""")

        val order = (repo.order("o-1") as CommerceResult.Success).value

        assertThat(order.canRetryPayment).isTrue()
        assertThat(order.canPayNow).isTrue()
        assertThat(order.deliveryAddress.city).isEmpty()
    }

    // ─── Payment ─────────────────────────────────────────────────────

    @Test
    fun `the three-state payment read maps status and refund_status`() = runBlocking {
        enqueue(
            """{"data":{"order_id":"o-1","status":"paid","amount_minor":25000,"currency":"INR",""" +
                """"refund_status":"pending","updated_at":"2026-09-13T06:31:05Z"},"meta":{}}""",
        )

        val payment = (repo.orderPayment("o-1") as CommerceResult.Success).value

        assertThat(payment.state).isEqualTo(OrderPaymentState.PAID)
        assertThat(payment.refundStatus).isEqualTo("pending")
        assertThat(payment.amount).isEqualTo(Paise(25_000))
    }

    @Test
    fun `a server without the three-state route answers NotAvailable`() = runBlocking {
        enqueue("""{"error":{}}""", code = 404)

        val result = repo.orderPayment("o-1") as CommerceResult.Failure

        assertThat(result.error).isEqualTo(CommerceError.NotAvailable)
        assertThat(result.error.isPermanentRefusal()).isFalse()
    }

    // ─── Errors ──────────────────────────────────────────────────────

    @Test
    fun `an unmapped 4xx carries its status, and is a permanent refusal`() = runBlocking {
        enqueue("""{"error":{"code":"NOT_YOUR_ORDER","message":"no"}}""", code = 403)

        val result = repo.orderPayment("o-1") as CommerceResult.Failure

        assertThat(result.error).isEqualTo(CommerceError.Unexpected("NOT_YOUR_ORDER", "no", httpStatus = 403))
        assertThat(result.error.isPermanentRefusal()).isTrue()
    }

    @Test
    fun `a 5xx is not a permanent refusal, and neither is a 429`() = runBlocking {
        enqueue("""{"error":{"code":"INTERNAL_ERROR","message":"boom"}}""", code = 500)
        enqueue("""{"error":{"code":"RATE_LIMITED","message":"slow down"}}""", code = 429)

        val server = repo.orderPayment("o-1") as CommerceResult.Failure
        val limited = repo.orderPayment("o-1") as CommerceResult.Failure

        assertThat(server.error.isPermanentRefusal()).isFalse()
        assertThat(limited.error.isPermanentRefusal()).isFalse()
    }

    @Test
    fun `ORDER_NOT_FOUND is a permanent refusal`() = runBlocking {
        enqueue("""{"error":{"code":"ORDER_NOT_FOUND","message":"order not found"}}""", code = 404)

        val result = repo.orderPayment("o-1") as CommerceResult.Failure

        assertThat(result.error).isEqualTo(CommerceError.OrderNotFound)
        assertThat(result.error.isPermanentRefusal()).isTrue()
    }

    /**
     * `call{}` used to catch Throwable, so a cancelled coroutine came back as
     * `Failure(Network(CancellationException))` and the caller carried on —
     * a ViewModel being cleared would render a network error into a state
     * nobody was looking at. Cancellation must propagate: the code after the
     * call must NOT run.
     */
    @Test
    fun `a cancelled coroutine is not a network failure`() = runBlocking {
        // A response that arrives too late: the call is cancelled mid-flight.
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body("""{"data":{"items":[]}}""")
                .headersDelay(10, TimeUnit.SECONDS)
                .build(),
        )

        var returned: CommerceResult<*>? = null
        var thrown: Throwable? = null
        val job = launch(Dispatchers.IO) {
            try {
                returned = repo.products()
            } catch (e: CancellationException) {
                thrown = e
            }
        }
        // Let the request reach the wire, then pull the plug.
        withTimeout(5_000) { while (server.requestCount == 0) delay(20) }
        job.cancelAndJoin()

        assertThat(thrown).isInstanceOf(CancellationException::class.java)
        assertThat(returned).isNull()
    }
}
