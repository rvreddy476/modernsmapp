package com.us.android.core.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.memberships.data.MembershipsApi
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.File

/**
 * What the app SENDS for memberships, byte for byte against the Go request
 * structs: `SubscribeRequest` and the Idempotency-Key header
 * (http/membership_payments_handler.go:171, `idempotencyKey`),
 * `CreateTierRequest` / `UpdateTierRequest` (http/handler.go:865, :972 — a
 * PATCH leaves out what it does not change), `UpgradeSubscriptionRequest`
 * (:232) and `CancelSubscriptionRequest` (http/subscription_handler.go:94).
 */
class MembershipsRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: MembershipsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(MembershipsApi::class.java)
        repository = MembershipsRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun answer(name: String, code: Int = 200) {
        val body = File("src/test/resources/contracts/memberships/$name").readText()
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun RecordedRequest.json(): JsonObject = this@MembershipsRequestTest.json.parseToJsonElement(
        body!!.utf8()
    ).jsonObject

    @Test
    fun `a join names the level and carries the decision's Idempotency-Key`() {
        answer("checkout_join.json", code = 201)
        runBlocking { repository.join("creator-a1", "tier-1", "key-abc") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/monetization/subscribe/creator-a1")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("key-abc")
        assertThat(request.json()).isEqualTo(JsonObject(mapOf("tier_id" to JsonPrimitive("tier-1"))))
    }

    @Test
    fun `a resend of the same decision sends the same key`() {
        answer("checkout_join.json", code = 201)
        answer("checkout_join.json", code = 201)
        runBlocking {
            repository.join("creator-a1", "tier-1", "key-abc")
            repository.join("creator-a1", "tier-1", "key-abc")
        }
        assertThat(server.takeRequest().headers["Idempotency-Key"]).isEqualTo("key-abc")
        assertThat(server.takeRequest().headers["Idempotency-Key"]).isEqualTo("key-abc")
    }

    @Test
    fun `a new level sends its name, price in paise, rupees, and the perks trimmed with blanks dropped`() {
        server.enqueue(
            MockResponse.Builder().code(
                201
            ).body("""{"data":{"id":"t1","name":"Supporter","price_paise":9900,"is_active":true}}""").build()
        )
        runBlocking { repository.createLevel("  Supporter ", 9_900, listOf(" Early access ", "", "  ")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/monetization/tiers")
        assertThat(request.json()).isEqualTo(
            JsonObject(
                mapOf(
                    "name" to JsonPrimitive("Supporter"),
                    "price_paise" to JsonPrimitive(9_900),
                    "currency" to JsonPrimitive("INR"),
                    "perks" to JsonArray(listOf(JsonPrimitive("Early access"))),
                ),
            ),
        )
    }

    @Test
    fun `turning a level off sends is_active alone, and an edit leaves out what it does not change`() {
        server.enqueue(
            MockResponse.Builder().body(
                """{"data":{"status":"updated","tier":{"id":"t1","is_active":false}}}"""
            ).build()
        )
        server.enqueue(MockResponse.Builder().body("""{"data":{"status":"updated","tier":{"id":"t1"}}}""").build())
        runBlocking {
            repository.updateLevel("t1", isActive = false)
            repository.updateLevel("t1", pricePaise = 14_900)
        }

        val off = server.takeRequest()
        assertThat(off.method).isEqualTo("PATCH")
        assertThat(off.target).isEqualTo("/v1/monetization/tiers/t1")
        assertThat(off.json()).isEqualTo(JsonObject(mapOf("is_active" to JsonPrimitive(false))))
        assertThat(server.takeRequest().json()).isEqualTo(JsonObject(mapOf("price_paise" to JsonPrimitive(14_900))))
    }

    @Test
    fun `renew, change level and cancel go to the subscription`() {
        answer("checkout_join.json", code = 201)
        answer("upgrade_scheduled.json")
        server.enqueue(MockResponse.Builder().body("""{"data":{"status":"cancelled_at_period_end"}}""").build())
        runBlocking {
            repository.renew("sub-1")
            repository.changeLevel("sub-1", "tier-2")
            repository.cancel("sub-1")
        }

        val renew = server.takeRequest()
        assertThat(renew.method).isEqualTo("POST")
        assertThat(renew.target).isEqualTo("/v1/monetization/subscriptions/sub-1/renew")
        val change = server.takeRequest()
        assertThat(change.target).isEqualTo("/v1/monetization/subscriptions/sub-1/upgrade")
        assertThat(change.json()).isEqualTo(JsonObject(mapOf("tier_id" to JsonPrimitive("tier-2"))))
        val cancel = server.takeRequest()
        assertThat(cancel.target).isEqualTo("/v1/monetization/subscriptions/sub-1/cancel")
        // At the period's end: never `immediate: true`, which ends access now.
        assertThat(cancel.json()).isEqualTo(
            JsonObject(mapOf("reason" to JsonPrimitive(""), "immediate" to JsonPrimitive(false))),
        )
    }
}
