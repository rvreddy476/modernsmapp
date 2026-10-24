package com.us.android.feature.search.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.search.ui.savedMatch
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
 * Saved searches (2026-10-24) against search-service: SaveSearch (POST
 * /v1/search/saved, handler.go:706-735: `{query, search_type}` → 201 the row),
 * GetSavedSearches (handler.go:738-757: `{"items":[…]}`, `[]` for none) and
 * DeleteSavedSearch (handler.go:760-785: 404 NOT_FOUND when already gone).
 */
class SavedSearchesTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: SearchRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder().baseUrl(server.url("/")).client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType())).build()
            .create(SearchApi::class.java)
        repository = SearchRepository(api, ErrorMapper(json), json)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    @Test
    fun `the saved list reads the items, skipping blank rows`() = runBlocking {
        enqueue(
            200,
            """{"data":{"items":[
                {"id":"s1","user_id":"me","query":" sunsets ","search_type":"universal","saved_at":"2026-10-24T10:00:00Z"},
                {"id":"","query":"ghost"},
                {"id":"s2","query":""}
            ]}}""",
        )

        val saved = (repository.saved() as AppResult.Success).data

        assertThat(saved).containsExactly(SavedSearch("s1", "sunsets"))
        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/search/saved")
    }

    @Test
    fun `saving posts the trimmed words as a universal search`() = runBlocking {
        enqueue(201, """{"data":{"id":"s9","query":"jazz","search_type":"universal","saved_at":"x"}}""")

        val saved = (repository.save("  jazz ") as AppResult.Success).data

        assertThat(saved).isEqualTo(SavedSearch("s9", "jazz"))
        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("POST")
        assertThat(sent.body!!.utf8()).isEqualTo("""{"query":"jazz","search_type":"universal"}""")
    }

    @Test
    fun `unsaving one already gone is still a success`() = runBlocking {
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"Saved search not found"}}""")

        assertThat(repository.deleteSaved("s1")).isEqualTo(AppResult.Success(Unit))
        val sent = server.takeRequest()
        assertThat("${sent.method} ${sent.url.encodedPath}").isEqualTo("DELETE /v1/search/saved/s1")
    }

    @Test
    fun `a saved search matches the words on screen, case and spaces aside`() {
        val saved = listOf(SavedSearch("s1", "Sunsets"), SavedSearch("s2", "jazz club"))

        assertThat(savedMatch(saved, " sunsets ")?.id).isEqualTo("s1")
        assertThat(savedMatch(saved, "JAZZ CLUB")?.id).isEqualTo("s2")
        assertThat(savedMatch(saved, "jazz")).isNull()
        assertThat(savedMatch(saved, "  ")).isNull()
    }
}
