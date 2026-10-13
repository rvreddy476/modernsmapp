package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.PartnerOfferCategory
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.time.Instant

class PartnerOfferDecisionTest {
    @Test fun emptyAndMalformedReadbacksAreDifferent() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            val api = client(server)
            for (body in listOf("{\"data\":null}", "{\"data\":[]}")) {
                server.enqueue(MockResponse.Builder().body(body).build())
                assertThat(api.list(PartnerOfferCategory.MOPED)).isEmpty()
            }
            server.enqueue(MockResponse.Builder().body("{\"data\":{\"unexpected\":true}}").build())
            assertThat(runCatching { api.list(PartnerOfferCategory.MOPED) }.isFailure).isTrue()
        }
    }
    private val id = "04d05275-0eb0-4fb5-a8ab-2226606de622"
    private fun request(category: PartnerOfferCategory, test: Boolean = false) =
        PartnerOfferRequest(category, id, Instant.now().plusSeconds(20), test)
    private fun client(server: MockWebServer) = PartnerOfferDecision(Retrofit.Builder()
        .baseUrl(server.url("/")).addConverterFactory(Json.asConverterFactory("application/json".toMediaType())).build())
    private fun body(req: PartnerOfferRequest, status: String = "pending", expiry: Instant = req.expiresAt): String {
        val row = """{"id":"$id","status":"$status","expires_at":"$expiry"}"""
        val data = when(req.category) {
            PartnerOfferCategory.HOME -> """{"items":[$row]}"""
            PartnerOfferCategory.FOOD -> """{"offers":[$row]}"""
            else -> "[$row]"
        }
        return """{"data":$data}"""
    }
    @Test fun allCategoriesReadBackBeforeAcceptAndDecline() = runBlocking {
        for(category in PartnerOfferCategory.entries) for(accept in listOf(true, false)) {
            MockWebServer().use { server ->
                server.start()
                val req = request(category)
                server.enqueue(MockResponse.Builder().body(body(req)).build())
                server.enqueue(MockResponse.Builder().body("""{"data":{"status":"${if(accept) "accepted" else "rejected"}"}}""").build())
                client(server).submit(req, accept)
                assertThat(server.takeRequest().url.encodedPath).isEqualTo("/" + category.offersPath())
                val post = server.takeRequest()
                assertThat(post.method).isEqualTo("POST")
                assertThat(post.url.encodedPath).isEqualTo("/" + req.decisionPath(accept))
            }
        }
    }
    @Test fun invalidExpiredWithdrawnAndUnauthorizedOffersNeverPost() = runBlocking {
        for(case in listOf("expired", "withdrawn", "unauthorized", "missing", "network")) {
            MockWebServer().use { server ->
                server.start()
                val req = request(PartnerOfferCategory.HOME)
                val response = when(case) {
                    "expired" -> MockResponse.Builder().body(body(req, expiry = Instant.now().minusSeconds(1)))
                    "withdrawn" -> MockResponse.Builder().body(body(req, status = "cancelled"))
                    "unauthorized" -> MockResponse.Builder().code(401)
                    "network" -> MockResponse.Builder().code(503)
                    else -> MockResponse.Builder().body("""{"data":{"items":[]}}""")
                }
                server.enqueue(response.build())
                assertThat(runCatching { client(server).submit(req, true) }.isFailure).isTrue()
                assertThat(server.requestCount).isEqualTo(1)
            }
        }
    }
    @Test fun testRequestsCannotReachLiveApi() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            assertThat(runCatching { client(server).submit(request(PartnerOfferCategory.HOME, test = true), true) }.isFailure).isTrue()
            assertThat(server.requestCount).isEqualTo(0)
        }
    }
    @Test fun countdownUsesOriginalDeadlineAndCannotReviveExpiredRequest() {
        val now = Instant.parse("2026-10-11T10:00:00Z")
        val req = request(PartnerOfferCategory.HOME).copy(expiresAt = now.plusSeconds(20))
        assertThat(req.secondsLeft(now)).isEqualTo(20)
        assertThat(req.secondsLeft(now.plusSeconds(8))).isEqualTo(12)
        assertThat(req.secondsLeft(now.plusSeconds(20))).isEqualTo(0)
        assertThat(req.secondsLeft(now.plusSeconds(40))).isEqualTo(0)
    }
    @Test fun onlyKnownCategoriesValidIdsAndDeadlinesAreActionable() {
        val data = mapOf("type" to "captain.offer", "entity_id" to id, "expires_at" to Instant.now().plusSeconds(20).toString())
        assertThat(PartnerOfferRequest.parse(data)?.category).isEqualTo(PartnerOfferCategory.MOPED)
        assertThat(PartnerOfferRequest.parse(data + ("type" to "food_order_new"))).isNull()
        assertThat(PartnerOfferRequest.parse(data + ("entity_id" to "../../other"))).isNull()
        assertThat(PartnerOfferRequest.parse(data - "expires_at")).isNull()
    }
}
