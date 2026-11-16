package com.us.android.feature.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
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
 * The Official mark routes (2026-11-15), against profile-service
 * internal/http/official_verification.go and officialverify.OrgStatus (commit 0782f6619).
 */
class OfficialMarkContractTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: OfficialMarkRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(OfficialMarkApi::class.java)
        repository = OfficialMarkRepository(api, ErrorMapper(json), json)
    }

    @After
    fun tearDown() = server.close()

    private fun answer(code: Int, body: String) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    @Test
    fun `an OrgStatus with every field parses`() = runBlocking {
        answer(200, FULL_STATUS)

        val result = repository.status()

        assertThat(result).isEqualTo(
            AppResult.Success(
                OrgStatus(
                    isVerified = true,
                    verificationLevel = "person",
                    badgeFlags = 1,
                    organisationName = "Acme Media Private Limited",
                    applicationId = "6f1c2c3e-9d0b-4b8e-8a7d-2f3e4a5b6c7d",
                    status = "refused",
                    refusalCode = "LEGAL_NAME_MISMATCH",
                    codeExpiresAt = "2026-11-15T10:30:00Z",
                    next = "apply",
                ),
            ),
        )
        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/profiles/me/verification/organisation")
    }

    @Test
    fun `an apply sends one registration and leaves the other out`(): Unit = runBlocking {
        answer(201, """{"data":{"is_verified":true,"verification_level":"person","next":"email_code"}}""")

        val result = repository.apply(
            OrgApplicationBody(
                legalName = "Acme",
                gstin = "29ABCDE1234F1Z5",
                officialDomain = "acme.in",
                signatoryEmail = "legal@acme.in",
            ),
        )

        assertThat((result as AppResult.Success).data.next).isEqualTo("email_code")
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        val body = Json.parseToJsonElement(request.body!!.utf8()).jsonObject
        assertThat(body.keys).containsExactly("legal_name", "gstin", "official_domain", "signatory_email")
    }

    @Test
    fun `an apply with a certificate carries document_media_id`(): Unit = runBlocking {
        answer(201, """{"data":{"next":"email_code"}}""")

        repository.apply(
            OrgApplicationBody(
                legalName = "Acme",
                cin = "U12345KA2020PTC123456",
                officialDomain = "acme.in",
                signatoryEmail = "legal@acme.in",
                documentMediaId = "7c9e6679-7425-40de-944b-e07fc1f90ae7",
            ),
        )

        val body = Json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject
        assertThat(body.keys)
            .containsExactly("legal_name", "cin", "official_domain", "signatory_email", "document_media_id")
        assertThat(body["document_media_id"]!!.jsonPrimitive.content).isEqualTo("7c9e6679-7425-40de-944b-e07fc1f90ae7")
    }

    @Test
    fun `confirm and accept post to their own routes`() = runBlocking {
        answer(200, """{"data":{"next":""}}""")
        answer(200, """{"data":{"next":""}}""")

        repository.confirmEmail("123456")
        repository.acceptGovernment("INV-1")

        val confirm = server.takeRequest()
        assertThat(confirm.url.encodedPath).isEqualTo("/v1/profiles/me/verification/organisation/confirm-email")
        assertThat(confirm.body!!.utf8()).isEqualTo("""{"code":"123456"}""")
        val accept = server.takeRequest()
        assertThat(accept.url.encodedPath).isEqualTo("/v1/profiles/me/verification/government/accept")
        assertThat(accept.body!!.utf8()).isEqualTo("""{"invite_code":"INV-1"}""")
    }

    @Test
    fun `a refusal keeps the server's code and sentence, 503 included`() = runBlocking {
        answer(503, """{"error":{"code":"ORG_VERIFICATION_DISABLED","message":"Official marks are not open yet."}}""")

        val error = (repository.status() as AppResult.Failure).error

        assertThat(error.officialCode()).isEqualTo("ORG_VERIFICATION_DISABLED")
        assertThat(error.officialMessage()).isEqualTo("Official marks are not open yet.")
        assertThat((error as AppError.Unknown).statusCode).isEqualTo(503)
    }

    @Test
    fun `a 403 keeps its sentence too`() = runBlocking {
        answer(403, """{"error":{"code":"PERSON_VERIFICATION_REQUIRED","message":"Verify yourself first."}}""")

        val error = (repository.status() as AppResult.Failure).error

        assertThat(error.officialCode()).isEqualTo("PERSON_VERIFICATION_REQUIRED")
        assertThat(error.officialMessage()).isEqualTo("Verify yourself first.")
    }

    private companion object {
        val FULL_STATUS = """
            {"data":{
              "is_verified":true,
              "verification_level":"person",
              "badge_flags":1,
              "organisation_name":"Acme Media Private Limited",
              "application_id":"6f1c2c3e-9d0b-4b8e-8a7d-2f3e4a5b6c7d",
              "status":"refused",
              "refusal_code":"LEGAL_NAME_MISMATCH",
              "code_expires_at":"2026-11-15T10:30:00Z",
              "next":"apply"
            }}
        """.trimIndent()
    }
}
