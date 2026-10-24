package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.util.concurrent.CopyOnWriteArrayList

/**
 * The profile's About and Reposts tabs (2026-10-24), against the handlers:
 * profile-service GetAllAbout (internal/http/handler.go:688-712, the public
 * rows grouped by section) and GetUserLinks (handler.go:151, public_surface.go:140-160),
 * and post-service ListCreatorReposts (internal/http/handler.go:3567-3600,
 * `ListUserRepostsResult` service/post.go:4277-4294).
 */
class ProfilePanelsApiRequestTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: ProfilePanelsRepository
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private val answers = mutableMapOf<String, Pair<Int, String>>()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                val (code, body) = answers["${request.method} ${request.url.encodedPath}"] ?: (500 to "")
                return MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()
            }
        }
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        repository = ProfilePanelsRepository(retrofit.create(ProfilePanelsApi::class.java), ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `about reads the grouped public rows and the safe links`() = runBlocking {
        answers["GET /v1/profiles/u1/about"] = 200 to """{"data":{
            "education":[{"section":"education","item_id":"e1","data":{"title":"MIT","subtitle":"CS"},"sort_order":0}],
            "work":[{"section":"work","item_id":"w2","data":{"title":"Later job"},"sort_order":2},
                    {"section":"work","item_id":"w1","data":{"title":"Acme","subtitle":"Engineer","detail":"2020-2024"},"sort_order":1},
                    {"section":"work","item_id":"w3","data":{"title":"  "},"sort_order":3}]
        }}"""
        answers["GET /v1/profiles/u1/links"] = 200 to """{"data":[
            {"platform":"github","url":"https://github.com/ana","display_label":"","sort_order":2},
            {"platform":"site","url":"https://ana.dev","display_label":"My site","sort_order":1},
            {"platform":"old","url":"http://insecure.example","display_label":"Old","sort_order":0}
        ]}"""

        val about = (repository.about("u1") as AppResult.Success).data

        // The web's section order: work before education, whatever the map's order.
        assertThat(about.sections.map { it.section }).containsExactly("work", "education").inOrder()
        // Rows in sort order, and a blank row left out.
        assertThat(about.sections.first().items.map { it.itemId }).containsExactly("w1", "w2").inOrder()
        assertThat(about.sections.first().items.first().detail).isEqualTo("2020-2024")
        // Links by sort order, labelled, and only https.
        assertThat(about.links).containsExactly(
            PublicLink("My site", "https://ana.dev"),
            PublicLink("github", "https://github.com/ana"),
        ).inOrder()
    }

    @Test
    fun `a failed links read leaves About without links rather than failing it`() = runBlocking {
        answers["GET /v1/profiles/u1/about"] = 200 to """{"data":{}}"""

        val about = (repository.about("u1") as AppResult.Success).data

        assertThat(about.sections).isEmpty()
        assertThat(about.links).isEmpty()
    }

    @Test
    fun `a blocked or hidden profile's about fails the tab`() = runBlocking {
        answers["GET /v1/profiles/u1/about"] = 404 to """{"error":{"code":"NOT_FOUND","message":"x"}}"""

        assertThat(repository.about("u1")).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `reposts page through the cursor and carry the quote and the original`() = runBlocking {
        answers["GET /v1/creators/u1/reposts"] = 200 to """{"data":{"items":[
            {"repost_id":"r1","type":"quote","quote_text":"so true","original_post_id":"p1","created_at":"2026-10-24T10:00:00Z",
             "post":{"id":"p1","text":"the original","content_type":"post","author":{"id":"a","display_name":"Ana","username":"ana"}}},
            {"repost_id":"r2","type":"repost","original_post_id":"p2","created_at":"2026-10-23T10:00:00Z",
             "post":{"id":"p2","title":"A video","text":"desc","content_type":"long_video","author":{"id":"b","display_name":"","username":"bo"}}},
            {"repost_id":"r3","type":"repost","original_post_id":"p3","created_at":"2026-10-22T10:00:00Z"}
        ],"next_cursor":"c2"}}"""

        val page = (repository.reposts("u1") as AppResult.Success).data

        assertThat(page.items.map { it.repostId }).containsExactly("r1", "r2").inOrder()
        assertThat(page.items[0].quoteText).isEqualTo("so true")
        assertThat(page.items[0].excerpt).isEqualTo("the original")
        assertThat(page.items[0].originalAuthor).isEqualTo("Ana")
        assertThat(page.items[1].excerpt).isEqualTo("A video")
        assertThat(page.items[1].originalAuthor).isEqualTo("bo")
        assertThat(page.items[1].contentType).isEqualTo("long_video")
        assertThat(page.nextCursor).isEqualTo("c2")
        val sent = requests.single { it.url.encodedPath == "/v1/creators/u1/reposts" }
        assertThat(sent.url.queryParameter("limit")).isEqualTo("20")
        assertThat(sent.url.queryParameter("cursor")).isNull()
    }

    @Test
    fun `the last page has no cursor`() = runBlocking {
        answers["GET /v1/creators/u1/reposts"] = 200 to """{"data":{"items":[]}}"""

        val page = (repository.reposts("u1", cursor = "c2") as AppResult.Success).data

        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
        assertThat(requests.single().url.queryParameter("cursor")).isEqualTo("c2")
    }
}
