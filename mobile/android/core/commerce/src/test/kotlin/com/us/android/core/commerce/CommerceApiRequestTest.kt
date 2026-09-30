package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.AddFavouriteRequest
import com.us.android.core.commerce.network.AddToCartRequest
import com.us.android.core.commerce.network.AddressDto
import com.us.android.core.commerce.network.AdjustStockRequest
import com.us.android.core.commerce.network.CancelOrderRequest
import com.us.android.core.commerce.network.CheckoutRequest
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.CreateVariantRequest
import com.us.android.core.commerce.network.QuoteRequest
import com.us.android.core.commerce.network.UpdateCartItemRequest
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

/**
 * The buyer and catalogue routes on the wire: path, method, query and body,
 * pinned against the Retrofit declaration with a MockWebServer.
 *
 * These exist because the add-favourite used to be declared as
 * `POST /favourites/{id}`, a route commerce-service never registered, so
 * every heart tap was a 404 and the optimistic UI flipped it straight back.
 * A test on the declaration is the only thing that catches a path typo, a
 * missing header or a renamed body key before a device does. The contract
 * is commerce-contract.md (30 Sep 2026) and the handlers it names.
 */
class CommerceApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: CommerceApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CommerceApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    // ─── Favourites ──────────────────────────────────────────────────

    @Test
    fun `add favourite posts the collection path with the product id in the body`() {
        enqueue("""{"data":{"product_id":"p-1","is_favourite":true}}""")

        val answer = runBlocking { api.addFavourite(AddFavouriteRequest(productId = "p-1")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/favourites")
        assertThat(request.body!!.utf8()).isEqualTo("""{"product_id":"p-1"}""")

        // The server echoes what it now holds; decode it rather than Unit so
        // a future "not saved" answer can be honoured without a wire change.
        val dto = answer.body()!!.data!!
        assertThat(dto.productId).isEqualTo("p-1")
        assertThat(dto.isFavourite).isTrue()
    }

    @Test
    fun `remove favourite deletes the item path`() {
        enqueue("""{"data":{"product_id":"p-1","is_favourite":false}}""")

        runBlocking { api.removeFavourite("p-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/commerce/favourites/p-1")
    }

    @Test
    fun `the favourites list is a plain get on the collection`() {
        enqueue("""{"data":{"items":[],"next_cursor":null}}""")

        runBlocking { api.favourites() }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/favourites")
    }

    // ─── Catalogue ───────────────────────────────────────────────────

    @Test
    fun `the product list carries its query parameters and omits the absent ones`() {
        enqueue("""{"data":{"items":[]}}""")

        runBlocking { api.listProducts(query = "kettle", categoryId = "c-1", cursor = "abc", limit = 20) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/products?q=kettle&category_id=c-1&cursor=abc&limit=20")
    }

    @Test
    fun `a product is read by id`() {
        enqueue("""{"data":{"product":{"id":"p-1","title":"Kettle","seller_id":"s-1"},"variants":[]}}""")

        val answer = runBlocking { api.getProduct("p-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/products/p-1")
        assertThat(answer.body()!!.data!!.product.id).isEqualTo("p-1")
    }

    @Test
    fun `a product's variants are read from the public variants path as items`() {
        enqueue("""{"data":{"items":[{"id":"v-1","sku":"SKU-1","selling_price_minor":1000}]}}""")

        val answer = runBlocking { api.productVariants("p-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/products/p-1/variants")
        assertThat(answer.body()!!.data!!.items.single().id).isEqualTo("v-1")
    }

    @Test
    fun `the category tree is the categories route with tree=true`() {
        enqueue("""{"data":[{"id":"c-1","name":"Books","slug":"books","is_listable":false,"children":[]}]}""")

        val answer = runBlocking { api.categoryTree() }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/categories?tree=true")
        assertThat(answer.body()!!.data!!.single().isListable).isFalse()
    }

    // ─── Bag ─────────────────────────────────────────────────────────

    @Test
    fun `add to bag posts the variant and quantity`() {
        enqueue("""{"data":{"items":[]}}""")

        runBlocking { api.addToCart(AddToCartRequest(variantId = "v-1", quantity = 2)) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/cart/items")
        assertThat(request.body!!.utf8()).isEqualTo("""{"variant_id":"v-1","quantity":2}""")
    }

    @Test
    fun `a bag line is changed by variant on the by-variant path`() {
        enqueue("""{"data":{"items":[]}}""")

        runBlocking { api.updateCartItem("v-1", UpdateCartItemRequest(quantity = 3)) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.target).isEqualTo("/v1/commerce/cart/items/by-variant/v-1")
        assertThat(request.body!!.utf8()).isEqualTo("""{"quantity":3}""")
    }

    @Test
    fun `a bag line is removed by variant`() {
        enqueue("""{"data":{"items":[]}}""")

        runBlocking { api.removeCartItem("v-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/commerce/cart/items/v-1")
    }

    // ─── Addresses ───────────────────────────────────────────────────

    @Test
    fun `an address is posted with the server's field names`() {
        enqueue("""{"data":{"id":"a-1"}}""")

        runBlocking {
            api.addAddress(
                AddressDto(
                    id = "",
                    label = "Office",
                    contactName = "Asha",
                    phone = "9800000000",
                    line1 = "2 Test Road",
                    line2 = null,
                    landmark = null,
                    city = "Bengaluru",
                    state = "Karnataka",
                    postalCode = "560001",
                    isDefault = true,
                ),
            )
        }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/addresses")
        assertThat(request.body!!.utf8()).isEqualTo(
            """{"label":"Office","contact_name":"Asha","phone":"9800000000","address_line_1":"2 Test Road",""" +
                """"city":"Bengaluru","state":"Karnataka","postal_code":"560001","is_default":true}""",
        )
    }

    // ─── Quote, checkout, payment ────────────────────────────────────

    @Test
    fun `the quote body carries the address, the coupon and the payment method`() {
        enqueue("""{"data":{"quote_id":"q-1"}}""")

        runBlocking { api.quote(QuoteRequest(addressId = "a-1", couponCode = "SAVE10", paymentMethod = "upi")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/checkout/quote")
        assertThat(request.body!!.utf8())
            .isEqualTo("""{"address_id":"a-1","coupon_code":"SAVE10","payment_method":"upi"}""")
    }

    @Test
    fun `checkout is the v2 route with the Idempotency-Key header and the expected total`() {
        enqueue("""{"data":{"order_id":"o-1","order_number":"MS-1"}}""", code = 201)

        runBlocking {
            api.checkout(
                idempotencyKey = "key-1",
                body = CheckoutRequest(
                    addressId = "a-1",
                    quoteId = "q-1",
                    paymentMethod = "upi",
                    expectedTotalMinor = 203_900,
                ),
            )
        }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/v2/orders/checkout")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("key-1")
        assertThat(request.body!!.utf8()).isEqualTo(
            """{"address_id":"a-1","quote_id":"q-1","payment_method":"upi","expected_total_minor":203900}""",
        )
    }

    @Test
    fun `the payment intent is a post on the order with no body`() {
        enqueue("""{"data":{"payment_intent_id":"pi-1","amount_minor":203900}}""")

        runBlocking { api.openPayment("o-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/orders/o-1/payment/intent")
        assertThat(request.body?.size ?: 0).isEqualTo(0)
    }

    @Test
    fun `the three-state payment read is the order's payment path`() {
        enqueue(
            """{"data":{"order_id":"o-1","status":"paid","amount_minor":25000,"currency":"INR",""" +
                """"refund_status":null,"updated_at":"2026-09-13T06:31:05Z"},"meta":{}}""",
        )

        val answer = runBlocking { api.orderPayment("o-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/orders/o-1/payment")
        assertThat(answer.body()!!.data!!.status).isEqualTo("paid")
        assertThat(answer.body()!!.data!!.refundStatus).isNull()
    }

    @Test
    fun `the older payment status read keeps its path`() {
        enqueue("""{"data":{"order_id":"o-1","order_status":"payment_pending","payment_status":"pending"}}""")

        runBlocking { api.paymentStatus("o-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/orders/o-1/payment/status")
    }

    // ─── Orders ──────────────────────────────────────────────────────

    @Test
    fun `the order list carries the cursor when there is one`() {
        enqueue("""{"data":{"items":[],"next_cursor":null}}""")
        enqueue("""{"data":{"items":[],"next_cursor":null}}""")

        runBlocking {
            api.listOrders(cursor = null, limit = 20)
            api.listOrders(cursor = "page-2", limit = 20)
        }

        assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/orders?limit=20")
        assertThat(server.takeRequest().target).isEqualTo("/v1/commerce/orders?cursor=page-2&limit=20")
    }

    @Test
    fun `cancel posts the reason`() {
        enqueue("""{"data":{}}""")

        runBlocking { api.cancelOrder("o-1", CancelOrderRequest(reason = "Changed my mind")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/orders/o-1/cancel")
        assertThat(request.body!!.utf8()).isEqualTo("""{"reason":"Changed my mind"}""")
    }

    // ─── Seller ──────────────────────────────────────────────────────

    @Test
    fun `the seller's own catalogue is the seller products path`() {
        enqueue("""{"data":{"items":[],"total":0}}""")

        runBlocking { api.sellerProducts(status = null, limit = 50, offset = 0) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/products?limit=50&offset=0")
    }

    @Test
    fun `a stock adjustment patches the VARIANT's stock path with a signed delta`() {
        enqueue("""{"data":{"variant_id":"v-1","total_qty":12,"reserved_qty":2,"available":10}}""")

        runBlocking { api.adjustStock("v-1", AdjustStockRequest(delta = -2, reason = "damage")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/variants/v-1/stock")
        assertThat(request.body!!.utf8()).isEqualTo("""{"delta":-2,"reason":"damage"}""")
    }

    @Test
    fun `a product create carries category_id and tax_class_id and paise prices`() {
        enqueue("""{"data":{"id":"p-1","title":"Kettle"}}""", code = 201)

        runBlocking {
            api.createProduct(
                CreateProductRequest(
                    title = "Kettle",
                    taxClassId = "tax-18",
                    categoryId = "cat-kitchen",
                    variants = listOf(
                        CreateVariantRequest(
                            sku = "SKU-1",
                            mrpMinor = Paise(249_900),
                            sellingPriceMinor = Paise(199_900),
                            stockQty = 5,
                        ),
                    ),
                ),
            )
        }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/products")
        assertThat(request.body!!.utf8()).isEqualTo(
            """{"title":"Kettle","tax_class_id":"tax-18","category_id":"cat-kitchen","variants":""" +
                """[{"sku":"SKU-1","mrp_minor":249900,"selling_price_minor":199900,"stock_qty":5}]}""",
        )
    }

    @Test
    fun `a product create without a category sends no category_id key at all`() {
        enqueue("""{"data":{"id":"p-1"}}""", code = 201)

        runBlocking {
            api.createProduct(
                CreateProductRequest(
                    title = "Kettle",
                    taxClassId = "tax-18",
                    variants = listOf(CreateVariantRequest("SKU-1", Paise(100), Paise(100))),
                ),
            )
        }

        assertThat(server.takeRequest().body!!.utf8()).doesNotContain("category_id")
    }
}
