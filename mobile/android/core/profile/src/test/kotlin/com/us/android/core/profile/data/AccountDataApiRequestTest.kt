package com.us.android.core.profile.data

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
import java.io.ByteArrayOutputStream

/**
 * Download your data and Request verification (2026-10-24), against
 * auth-service ExportUserData (internal/http/handler.go:1311-1326: a bare JSON
 * file, `Content-Disposition: attachment`) and trust-safety
 * SubmitVerificationRequest (internal/http/trust_extras_handler.go:241-264:
 * `{type, docs}` → 201 the stored request; 400 BAD_REQUEST with a sentence).
 */
class AccountDataApiRequestTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: AccountDataRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(AccountDataApi::class.java)
        repository = AccountDataRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `the export file is streamed byte for byte into the picked file`() = runBlocking {
        val file = """{"user":{"id":"u1","email":"a@b.c"},"sessions":[]}"""
        server.enqueue(
            MockResponse.Builder().code(200)
                .setHeader("Content-Type", "application/json; charset=utf-8")
                .setHeader("Content-Disposition", "attachment; filename=data-export.json")
                .body(file).build(),
        )
        val out = ByteArrayOutputStream()

        val result = repository.exportTo(out)

        assertThat(result).isEqualTo(AppResult.Success(file.length.toLong()))
        assertThat(out.toString()).isEqualTo(file)
        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/auth/data-export")
    }

    @Test
    fun `a refused export is a failure, nothing written`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(401).body("""{"error":{"code":"UNAUTHORIZED"}}""").build())
        val out = ByteArrayOutputStream()

        assertThat(repository.exportTo(out)).isInstanceOf(AppResult.Failure::class.java)
        assertThat(out.size()).isEqualTo(0)
    }

    @Test
    fun `verification sends the kind and the note as docs, or no docs at all`() = runBlocking {
        repeat(2) {
            server.enqueue(
                MockResponse.Builder().code(201).setHeader("Content-Type", "application/json")
                    .body("""{"data":{"id":"v1","user_id":"u1","type":"creator","status":"pending","created_at":"x","updated_at":"x"}}""")
                    .build(),
            )
        }

        val status = repository.requestVerification(VerificationKind.CREATOR, "  I make films ")
        repository.requestVerification(VerificationKind.BUSINESS)

        assertThat(status).isEqualTo(AppResult.Success("pending"))
        val first = server.takeRequest()
        assertThat("${first.method} ${first.url.encodedPath}").isEqualTo("POST /v1/verification-requests")
        assertThat(first.body!!.utf8()).isEqualTo("""{"type":"creator","docs":{"note":"I make films"}}""")
        assertThat(server.takeRequest().body!!.utf8()).isEqualTo("""{"type":"business"}""")
    }

    @Test
    fun `a pending request is refused in the server's words`() = runBlocking {
        server.enqueue(
            MockResponse.Builder().code(400).setHeader("Content-Type", "application/json")
                .body("""{"error":{"code":"BAD_REQUEST","message":"a pending verification request already exists for this user"}}""")
                .build(),
        )

        val error = (repository.requestVerification(VerificationKind.CREATOR) as AppResult.Failure).error

        assertThat(verificationRefusal(error)).isEqualTo("You already have a request waiting for review.")
        assertThat(verificationRefusal(AppError.NoNetwork())).startsWith("You're offline")
    }
}
