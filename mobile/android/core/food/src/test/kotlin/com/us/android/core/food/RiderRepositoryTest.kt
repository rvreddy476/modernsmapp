package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.DeliveryDocumentRequest
import com.us.android.core.food.network.DeliveryLocationRequest
import com.us.android.core.food.network.RiderApi
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderAssignmentStep
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.food.repository.code
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

/** The rider routes on the wire: paths, headers, bodies, and the "nothing yet" 404s read as null. */
class RiderRepositoryTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: RiderRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/").toString())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(RiderApi::class.java)
        repository = RiderRepository(api, json)
    }

    @After
    fun tearDown() = server.close()

    private fun fixture(name: String) = File("src/test/resources/contracts/$name").readText()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).addHeader("Content-Type", "application/json").body(body).build())
    }

    @Test
    fun `verify-delivery posts the trimmed code to the assignment`() {
        enqueue(fixture("delivery_verify_delivery_post_422_code_invalid.json"), code = 422)

        val result = runBlocking { repository.verifyDelivery("a-1", " 7390 ") }

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/food/delivery/assignments/a-1/verify-delivery")
        assertThat(request.body?.utf8()).isEqualTo("""{"code":"7390"}""")
        assertThat((result as FoodResult.Failure).error.code).isEqualTo("FOOD_DELIVERY_CODE_INVALID")
    }

    @Test
    fun `no current job and no profile yet are empty successes, not failures`() {
        enqueue("""{"error":{"code":"FOOD_DELIVERY_CURRENT_NOT_FOUND","message":"no active assignment"}}""", code = 404)
        enqueue("""{"error":{"code":"FOOD_DELIVERY_PROFILE_NOT_FOUND","message":"delivery partner profile not found"}}""", code = 404)

        assertThat(runBlocking { repository.currentAssignment() }).isEqualTo(FoodResult.Success(null))
        assertThat(runBlocking { repository.profile() }).isEqualTo(FoodResult.Success(null))
    }

    @Test
    fun `assignment steps carry the idempotency key`() {
        enqueue(fixture("delivery_assignment_current_get_200_accepted.json"))

        runBlocking { repository.step("a-1", RiderAssignmentStep.ARRIVED_AT_RESTAURANT, "key-1") }

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/food/delivery/assignments/a-1/arrived-restaurant")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("key-1")
    }

    @Test
    fun `a location ping omits heading and accuracy when the fix has none`() {
        enqueue(fixture("delivery_location_post_200.json"))
        enqueue(fixture("delivery_location_post_200.json"))

        runBlocking {
            repository.postLocation(DeliveryLocationRequest(latitude = 12.9716, longitude = 77.5946))
            repository.postLocation(DeliveryLocationRequest(12.9716, 77.5946, accuracyMeters = 8.5, heading = 90.0))
        }

        assertThat(server.takeRequest().body?.utf8()).isEqualTo("""{"latitude":12.9716,"longitude":77.5946}""")
        assertThat(server.takeRequest().body?.utf8())
            .isEqualTo("""{"latitude":12.9716,"longitude":77.5946,"accuracy_meters":8.5,"heading":90.0}""")
    }

    @Test
    fun `a selfie document carries no number, availability always sends the flag`() {
        enqueue(fixture("delivery_document_post_201_selfie.json"), code = 201)
        enqueue("""{"data":{"session":{"category":"food_delivery","revision":6},"reservations":[]}}""")
        enqueue("""{"data":{"id":"p-1","status":"OFFLINE","is_online":false}}""")

        runBlocking {
            repository.addDocument(DeliveryDocumentRequest(documentType = "SELFIE", mediaId = "m-1"))
            repository.setAvailability(online = false)
        }

        assertThat(server.takeRequest().body?.utf8()).isEqualTo("""{"document_type":"SELFIE","media_id":"m-1"}""")
        assertThat(server.takeRequest().target).isEqualTo("/v1/partner/work-session")
        val availability = server.takeRequest()
        assertThat(availability.target).isEqualTo("/v1/food/delivery/availability")
        assertThat(availability.body?.utf8()).isEqualTo("""{"is_online":false}""")
        assertThat(availability.headers["X-Work-Revision"]).isEqualTo("6")
    }

    @Test
    fun `a null offers list reads as empty`() {
        enqueue("""{"data":{"offers":null}}""")

        assertThat(runBlocking { repository.offers() }).isEqualTo(FoodResult.Success(emptyList<Any>()))
    }

    @Test
    fun `a batch reads the saved sequence and a solo order reads as null`() {
        enqueue("""{"data":{"id":"b-1","restaurant_id":"r-1","status":"assigned","created_at":"now","members":[{"order_id":"o-1","sequence":1},{"order_id":"o-2","sequence":2}]}}""")
        enqueue("""{"error":{"code":"BATCH_NOT_FOUND","message":"batch not found"}}""", code = 404)
        val batch = runBlocking { repository.batch("o-1") } as FoodResult.Success
        assertThat(batch.value?.members?.map { it.orderId }).containsExactly("o-1", "o-2").inOrder()
        assertThat(server.takeRequest().target).isEqualTo("/v1/food/delivery/orders/o-1/batch")
        assertThat(runBlocking { repository.batch("solo") }).isEqualTo(FoodResult.Success(null))
    }

    @Test
    fun `assignments uses own list endpoint and coalesces null items`() {
        enqueue("""{"data":{"items":null}}""")
        assertThat(runBlocking { repository.assignments() }).isEqualTo(FoodResult.Success(emptyList<Any>()))
        assertThat(server.takeRequest().target).isEqualTo("/v1/food/delivery/assignments")
    }

    @Test
    fun `route preview is a bodyless advisory request and not an assignment mutation`() {
        enqueue("""{"data":{"order_ids":["o-2","o-1"],"source":"google_route_optimization","advisory":true,"note":"suggestion only"}}""")
        val result = runBlocking { repository.previewBatchRoute("o-1") } as FoodResult.Success
        assertThat(result.value.orderIds).containsExactly("o-2", "o-1").inOrder()
        assertThat(result.value.advisory).isTrue()
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/food/delivery/orders/o-1/batch/route-preview")
        assertThat(request.body?.utf8().orEmpty()).isEmpty()
    }

    @Test
    fun `route failures remain failures instead of masquerading as empty suggestions`() {
        for ((status, code) in listOf(404 to "BATCH_NOT_FOUND", 429 to "BATCH_ROUTE_BUSY", 503 to "BATCH_ROUTE_UNAVAILABLE")) {
            enqueue("""{"error":{"code":"$code","message":"try again"}}""", status)
            val result = runBlocking { repository.previewBatchRoute("o-1") } as FoodResult.Failure
            assertThat(result.error.code).isEqualTo(code)
        }
    }
}
