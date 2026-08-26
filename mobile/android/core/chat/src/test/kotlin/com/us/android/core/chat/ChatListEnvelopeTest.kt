package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
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
 * The list-envelope contract for the NON-PAGINATED chat list endpoints —
 * requests, invitations and connections (review Fix-1 residual).
 *
 * Go handlers hand the envelope a slice that is `nil` for zero rows
 * (`var out []Conversation` — messenger_extras.go:436, groups.go:567), which
 * serializes as `"data":null` or an absent key. Through the REAL Retrofit
 * stack these bytes must decode to an EMPTY list, while an error object must
 * stay a failure. Routing these calls through plain `apiCall` turned every
 * empty state into a Malformed failure.
 */
class ChatListEnvelopeTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: ChatRepository

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ChatApi::class.java)
        repository = ChatRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(MockResponse.Builder().code(200).body(body).build())
    }

    @Test
    fun `an empty requests inbox arrives as data null and is an empty list`() = runTest {
        enqueue("""{"data":null}""")

        val result = repository.requests()

        assertThat((result as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `a requests envelope with data absent is an empty list`() = runTest {
        enqueue("""{"meta":{"request_id":"r-1"}}""")

        val result = repository.requests()

        assertThat((result as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `a requests error object stays a failure`() = runTest {
        enqueue("""{"error":{"code":"FORBIDDEN","message":"no"},"meta":{"request_id":"r-2"}}""")

        val result = repository.requests()

        val error = (result as AppResult.Failure).error
        assertThat((error as AppError.Unknown).code).isEqualTo("FORBIDDEN")
    }

    @Test
    fun `an empty invitations list arrives as data null and is an empty list`() = runTest {
        enqueue("""{"data":null}""")

        val result = repository.invitations()

        assertThat((result as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `an invitations error object stays a failure`() = runTest {
        enqueue("""{"error":{"code":"AUTH_FAILED","message":"expired"}}""")

        assertThat(repository.invitations()).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `empty connections arrive as data null and are an empty list`() = runTest {
        enqueue("""{"data":null}""")

        val result = repository.connections("viewer-1")

        assertThat((result as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `a populated connections list still decodes`() = runTest {
        enqueue("""{"data":["11111111-2222-3333-4444-555555555555"]}""")

        val result = repository.connections("viewer-1")

        assertThat((result as AppResult.Success).data)
            .containsExactly("11111111-2222-3333-4444-555555555555")
    }
}
