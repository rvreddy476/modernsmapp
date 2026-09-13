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
        enqueue("""{"data":{"id":"p-1","status":"OFFLINE","is_online":false}}""")

        runBlocking {
            repository.addDocument(DeliveryDocumentRequest(documentType = "SELFIE", mediaId = "m-1"))
            repository.setAvailability(online = false)
        }

        assertThat(server.takeRequest().body?.utf8()).isEqualTo("""{"document_type":"SELFIE","media_id":"m-1"}""")
        val availability = server.takeRequest()
        assertThat(availability.target).isEqualTo("/v1/food/delivery/availability")
        assertThat(availability.body?.utf8()).isEqualTo("""{"is_online":false}""")
    }

    @Test
    fun `a null offers list reads as empty`() {
        enqueue("""{"data":{"offers":null}}""")

        assertThat(runBlocking { repository.offers() }).isEqualTo(FoodResult.Success(emptyList<Any>()))
    }
}
