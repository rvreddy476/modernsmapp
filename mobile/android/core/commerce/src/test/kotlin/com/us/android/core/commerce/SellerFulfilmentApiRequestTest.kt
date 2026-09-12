package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.network.RejectReturnRequest
import com.us.android.core.commerce.network.SellerCancelOrderRequest
import com.us.android.core.commerce.network.ShipOrderRequest
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
 * The seller order, return and earnings routes on the wire.
 *
 * A test on the Retrofit declaration is the only thing that catches a path
 * or body-shape mistake before a device does, and this surface has two
 * shapes that are easy to get wrong from memory: the order list pages by
 * `offset`, not a cursor, and approve/reject live under `/returns/`, not
 * `/seller/returns/`.
 */
class SellerFulfilmentApiRequestTest {

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

    @Test
    fun `the order list is a get paged by limit and offset`() {
        enqueue("""{"data":[]}""")

        runBlocking { api.sellerOrders(limit = 20, offset = 40) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/orders?limit=20&offset=40")
    }

    @Test
    fun `one order is read from the seller path by id`() {
        enqueue("""{"data":{"order":{"id":"o-1"},"items":[]}}""")

        val answer = runBlocking { api.sellerOrder("o-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/orders/o-1")
        assertThat(answer.body()!!.data!!.order.id).isEqualTo("o-1")
    }

    @Test
    fun `pack posts to the order with no body and reads the transition result`() {
        enqueue("""{"data":{"order_id":"o-1","status":"packed","applied":true}}""")
        // A repeat: 200, applied false. Still a success on this side.
        enqueue("""{"data":{"order_id":"o-1","status":"packed","applied":false}}""")

        val (first, repeat) = runBlocking { api.packOrder("o-1") to api.packOrder("o-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/orders/o-1/pack")
        assertThat(first.body()!!.data!!.applied).isTrue()
        assertThat(first.body()!!.data!!.status).isEqualTo("packed")
        assertThat(repeat.isSuccessful).isTrue()
        assertThat(repeat.body()!!.data!!.applied).isFalse()
    }

    @Test
    fun `ship posts the courier and tracking number in snake case and reads the shipments at 201`() {
        enqueue(
            """
            {"data":{"shipments":[{"id":"s-1","order_id":"o-1","seller_id":"sel-1","courier":"delhivery",
             "tracking_number":"DL123456789","courier_order_id":null,"label_url":null,"tracking_url":null,
             "status":"created","eta":null,"shipped_at":null,"delivered_at":null,"last_event_at":null,
             "created_at":"2026-09-12T12:00:00Z","updated_at":"2026-09-12T12:00:00Z"}]}}
            """.trimIndent(),
            code = 201,
        )

        val answer = runBlocking {
            api.shipOrder("o-1", ShipOrderRequest(courier = "delhivery", trackingNumber = "DL123456789"))
        }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/orders/o-1/ship")
        assertThat(request.body!!.utf8())
            .isEqualTo("""{"courier":"delhivery","tracking_number":"DL123456789"}""")
        assertThat(answer.code()).isEqualTo(201)
        val shipment = answer.body()!!.data!!.shipments.single()
        assertThat(shipment.courier).isEqualTo("delhivery")
        assertThat(shipment.trackingNumber).isEqualTo("DL123456789")
    }

    @Test
    fun `a seller cancel posts the reason to the seller path and reads where the order landed`() {
        // Not /orders/{id}/cancel: that route acts as the CUSTOMER and would
        // refuse a seller as not the owner.
        enqueue("""{"data":{"order_id":"o-1","status":"refund_pending","applied":true}}""")

        val answer = runBlocking { api.sellerCancelOrder("o-1", SellerCancelOrderRequest("Out of stock")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/orders/o-1/cancel")
        assertThat(request.body!!.utf8()).isEqualTo("""{"reason":"Out of stock"}""")
        assertThat(answer.body()!!.data!!.status).isEqualTo("refund_pending")
    }

    @Test
    fun `the order history is a get on the seller path`() {
        enqueue(
            """
            {"data":{"order_id":"o-1","history":[
              {"to_status":"payment_pending","actor_type":"customer","created_at":"2026-09-12T10:15:00Z"},
              {"from_status":"payment_pending","to_status":"confirmed","actor_type":"system",
               "created_at":"2026-09-12T10:16:00Z"}]}}
            """.trimIndent(),
        )

        val answer = runBlocking { api.sellerOrderHistory("o-1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/orders/o-1/history")
        val history = answer.body()!!.data!!.history
        assertThat(history).hasSize(2)
        assertThat(history[0].fromStatus).isNull()
        assertThat(history[1].toStatus).isEqualTo("confirmed")
    }

    @Test
    fun `the returns inbox is a get with an optional status`() {
        enqueue("""{"data":{"returns":[],"status":"requested"}}""")
        enqueue("""{"data":{"returns":[],"status":""}}""")

        runBlocking {
            api.sellerReturns(status = "requested", limit = 20, offset = 0)
            api.sellerReturns(status = null, limit = 20, offset = 0)
        }

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/commerce/seller/returns?status=requested&limit=20&offset=0")
        // A null status is OMITTED, not sent as "null": the server filters
        // on the raw string and "null" matches nothing.
        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/commerce/seller/returns?limit=20&offset=0")
    }

    @Test
    fun `approve and reject post to the returns path, and reject carries a reason`() {
        enqueue("""{"data":{"id":"r-1","status":"approved"}}""")
        enqueue("""{"data":{"id":"r-1","status":"rejected"}}""")

        runBlocking {
            api.approveReturn("r-1")
            api.rejectReturn("r-1", RejectReturnRequest("Item was used"))
        }

        val approve = server.takeRequest()
        assertThat(approve.method).isEqualTo("POST")
        assertThat(approve.target).isEqualTo("/v1/commerce/returns/r-1/approve")

        val reject = server.takeRequest()
        assertThat(reject.method).isEqualTo("POST")
        assertThat(reject.target).isEqualTo("/v1/commerce/returns/r-1/reject")
        assertThat(reject.body!!.utf8()).isEqualTo("""{"reason":"Item was used"}""")
    }

    @Test
    fun `earnings is a get paged by limit and offset`() {
        enqueue("""{"data":{"earnings":[]}}""")

        runBlocking { api.sellerEarnings(limit = 50, offset = 0) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/commerce/seller/earnings?limit=50&offset=0")
    }
}
