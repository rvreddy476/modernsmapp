package com.us.android.feature.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * The person-verification routes (2026-11-15), against profile-service
 * internal/http/person_verification.go and personverify.Status / StartResult.
 */
class PersonVerificationContractTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: PersonVerificationRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(PersonVerificationApi::class.java)
        repository = PersonVerificationRepository(api, ErrorMapper(json), json)
    }

    @After
    fun tearDown() = server.close()

    private fun answer(code: Int, body: String) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    @Test
    fun `a status with an attempt parses`() = runBlocking {
        answer(200, FULL_STATUS)

        val result = repository.status()

        assertThat(result).isEqualTo(
            AppResult.Success(
                PersonStatus(
                    isVerified = false,
                    verificationLevel = "",
                    badgeFlags = 0,
                    attempt = PersonAttempt(
                        id = "6f1c2c3e-9d0b-4b8e-8a7d-2f3e4a5b6c7d",
                        status = "refused",
                        refusalCode = "FACE_MISMATCH",
                        expiresAt = "2026-11-15T10:30:00Z",
                    ),
                    next = "digilocker",
                ),
            ),
        )
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.url.encodedPath).isEqualTo("/v1/profiles/me/verification/person")
    }

    @Test
    fun `a status with no attempt parses the null`() = runBlocking {
        answer(
            200,
            """{"data":{"is_verified":true,"verification_level":"person","badge_flags":1,"attempt":null,"next":""}}""",
        )

        val status = (repository.status() as AppResult.Success).data

        assertThat(status.attempt).isNull()
        assertThat(status.next).isEmpty()
        assertThat(status.isVerified).isTrue()
    }

    @Test
    fun `start posts an empty object and reads the authorize url`() = runBlocking {
        answer(
            201,
            """{"data":{"attempt_id":"a1","authorize_url":"https://digilocker.example/authorize?x=1",""" +
                """"expires_at":"2026-11-15T10:30:00Z"}}""",
        )

        val start = (repository.start() as AppResult.Success).data

        assertThat(start).isEqualTo(
            PersonStart(
                attemptId = "a1",
                authorizeUrl = "https://digilocker.example/authorize?x=1",
                expiresAt = "2026-11-15T10:30:00Z",
            ),
        )
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.url.encodedPath).isEqualTo("/v1/profiles/me/verification/person/start")
        assertThat(request.body!!.utf8()).isEqualTo("{}")
    }

    @Test
    fun `callback and selfie post to their own routes`() = runBlocking {
        answer(200, """{"data":{"next":"selfie"}}""")
        answer(200, """{"data":{"next":""}}""")

        repository.callback(state = "st", code = "cd")
        repository.selfie("9b2e7f0a-1c3d-4e5f-8a9b-0c1d2e3f4a5b")

        val callback = server.takeRequest()
        assertThat(callback.url.encodedPath).isEqualTo("/v1/profiles/me/verification/person/callback")
        assertThat(callback.body!!.utf8()).isEqualTo("""{"state":"st","code":"cd"}""")
        val selfie = server.takeRequest()
        assertThat(selfie.url.encodedPath).isEqualTo("/v1/profiles/me/verification/person/selfie")
        assertThat(selfie.body!!.utf8()).isEqualTo("""{"selfie_media_id":"9b2e7f0a-1c3d-4e5f-8a9b-0c1d2e3f4a5b"}""")
    }

    @Test
    fun `a refusal keeps the server's code and sentence, 503 included`() = runBlocking {
        answer(
            503,
            """{"error":{"code":"PERSON_VERIFICATION_DISABLED","message":"Person verification is not open yet."}}""",
        )

        val error = (repository.status() as AppResult.Failure).error

        assertThat(error.officialCode()).isEqualTo("PERSON_VERIFICATION_DISABLED")
        assertThat(error.officialMessage()).isEqualTo("Person verification is not open yet.")
        assertThat((error as AppError.Unknown).statusCode).isEqualTo(503)
    }

    @Test
    fun `a 422 name mismatch keeps its sentence`() = runBlocking {
        answer(
            422,
            """{"error":{"code":"NAME_MISMATCH","message":"Your display name must match the name on your Aadhaar"}}""",
        )

        val error = (repository.callback(state = "s", code = "c") as AppResult.Failure).error

        assertThat(error.officialCode()).isEqualTo("NAME_MISMATCH")
        assertThat(error.officialMessage()).isEqualTo("Your display name must match the name on your Aadhaar")
    }

    @Test
    fun `a refusal with no body falls back to the plain line`() = runBlocking {
        answer(502, "")

        val error = (repository.start() as AppResult.Failure).error

        assertThat(error.officialMessage()).isEqualTo(OFFICIAL_FALLBACK_MESSAGE)
    }

    private companion object {
        val FULL_STATUS = """
            {"data":{
              "is_verified":false,
              "verification_level":"",
              "badge_flags":0,
              "attempt":{
                "id":"6f1c2c3e-9d0b-4b8e-8a7d-2f3e4a5b6c7d",
                "status":"refused",
                "refusal_code":"FACE_MISMATCH",
                "expires_at":"2026-11-15T10:30:00Z"
              },
              "next":"digilocker"
            }}
        """.trimIndent()
    }
}
