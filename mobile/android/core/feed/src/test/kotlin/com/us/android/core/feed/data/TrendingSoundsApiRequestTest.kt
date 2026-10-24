package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ErrorMapper
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
 * Trending sounds' two reads (2026-10-24), byte for byte, through the app's
 * own Json and Retrofit.
 *
 * What this protects: the query (`filter`, `limit`, and the cursor only
 * after the first page), `meta`'s cursor and time reaching the page, an
 * answer in the route's OLD shape being an empty list rather than a
 * failure, and a sound read's badge being null — never an error — when the
 * read fails.
 */
class TrendingSoundsApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: TrendingSoundsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(TrendingSoundsApi::class.java)
        repository = TrendingSoundsRepository(api, ErrorMapper(json), TEST_CONFIG)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private val body = """
        {"data":[{"rank":1,"sound":{"id":"s1","title":"Morning run","artist":"Asha","duration_ms":28400,
          "sample_rate":44100,"status":"ready","is_original":true,"license_type":"original","usage_count":7,
          "source_media_id":"m1","source_post_id":"p1","source_reel_id":"p1","creator_user_id":"u1",
          "created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-24T10:00:00Z"},
          "uses_7d":42,"uses_prev_7d":10,"rising":true}],
         "meta":{"next_cursor":"c2","updated_at":"2026-10-24T10:15:00Z"}}
    """.trimIndent()

    @Test
    fun `the first page asks for the filter and the page size, with no cursor`() {
        enqueue(body)

        val result = runBlocking { repository.page(TrendingFilter.ALL) }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("GET")
        assertThat(sent.target).isEqualTo("/v1/audio/trending?filter=all&limit=20")
        val page = (result as AppResult.Success).data
        assertThat(page.sounds.single().sound.id).isEqualTo("s1")
        assertThat(page.sounds.single().uses7d).isEqualTo(42L)
        assertThat(page.nextCursor).isEqualTo("c2")
        assertThat(page.updatedAt).isEqualTo("2026-10-24T10:15:00Z")
    }

    @Test
    fun `a gateway cover lands on the API origin, an https cover is kept, anything else is dropped`() {
        enqueue(
            """{"data":[
              {"rank":1,"sound":{"id":"a","cover_url":"/v1/media/m1/serve/thumb_150"}},
              {"rank":2,"sound":{"id":"b","cover_url":"https://cdn.test/art.jpg"}},
              {"rank":3,"sound":{"id":"c","cover_url":"//evil.test/x.jpg"}},
              {"rank":4,"sound":{"id":"d","cover_url":null}}]}""",
        )

        val page = (runBlocking { repository.page(TrendingFilter.ALL) } as AppResult.Success).data

        assertThat(page.sounds.map { it.coverUrl }).containsExactly(
            "https://api.test/v1/media/m1/serve/thumb_150",
            "https://cdn.test/art.jpg",
            null,
            null,
        ).inOrder()
    }

    @Test
    fun `a later page replays the cursor verbatim, and Rising is asked by its wire name`() {
        enqueue("""{"data":[],"meta":{}}""")

        val result = runBlocking { repository.page(TrendingFilter.RISING, cursor = "abc==") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/audio/trending?filter=rising&limit=20&cursor=abc%3D%3D")
        assertThat((result as AppResult.Success).data.nextCursor).isNull()
    }

    @Test
    fun `the limit is held to the server's ceiling`() {
        enqueue("""{"data":[]}""")

        runBlocking { repository.page(TrendingFilter.ALL, limit = 500) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/audio/trending?filter=all&limit=50")
    }

    @Test
    fun `the route's old tracks shape is an empty list, not a failure`() {
        enqueue("""{"data":{"tracks":[{"id":"s1","title":"Old"}]}}""")

        val result = runBlocking { repository.page(TrendingFilter.ALL) }

        assertThat((result as AppResult.Success).data.sounds).isEmpty()
    }

    @Test
    fun `a refusal is a failure the list can say`() {
        enqueue("""{"error":{"code":"SERVICE_UNAVAILABLE","message":"m"}}""", 503)

        val result = runBlocking { repository.page(TrendingFilter.ALL) }

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError::class.java)
    }

    @Test
    fun `a sound's mark is read from its own row`() {
        enqueue("""{"data":{"id":"s1","title":"x","trending":{"rank":3,"rising":false}}}""")

        val mark = runBlocking { repository.mark("s1") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/audio/s1")
        assertThat(mark).isEqualTo(TrendingMark(rank = 3, rising = false))
    }

    @Test
    fun `a failed or missing read is no mark, and a blank id is not asked`() {
        enqueue("""{"error":{"code":"NOT_FOUND","message":"m"}}""", 404)
        assertThat(runBlocking { repository.mark("s1") }).isNull()

        enqueue("""{"data":{"id":"s1"}}""")
        assertThat(runBlocking { repository.mark("s1") }).isNull()

        assertThat(runBlocking { repository.mark(" ") }).isNull()
        assertThat(server.requestCount).isEqualTo(2)
    }

    private companion object {
        val TEST_CONFIG = ApiConfig(
            baseUrl = "https://api.test/",
            wsBaseUrl = "wss://ws.test",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        )
    }
}
