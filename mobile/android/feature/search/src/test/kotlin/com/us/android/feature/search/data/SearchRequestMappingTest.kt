package com.us.android.feature.search.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Each tab on its own route, on the wire (T-15/T-16): the real [SearchApi]
 * through Retrofit against MockWebServer, the `Json` the app uses. The one
 * rule the server depends on is that `record=1` goes ONLY with People and
 * Hashtags — those routes also serve pickers whose keystrokes are not
 * searches — and never with Top, Videos, Reels or Sounds.
 */
class SearchRequestMappingTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var repository: SearchRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        client = OkHttpClient()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(SearchApi::class.java)
        repository = SearchRepository(api, ErrorMapper(json), json)
    }

    @After
    fun tearDown() {
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
        server.close()
    }

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(
            MockResponse.Builder()
                .code(code)
                .body(body)
                .setHeader("Content-Type", "application/json")
                .build(),
        )
    }

    private fun RecordedRequest.url(): HttpUrl = server.url(target)

    private fun search(scope: SearchScope, query: String = "cats"): Pair<AppResult<List<SearchHit>>, HttpUrl> {
        val result = runBlocking { repository.search(scope, query) }
        return result to server.takeRequest().url()
    }

    // ── Request mapping, per tab ───────────────────────────────────────

    @Test
    fun `Top is the ranked search over posts, people and tags, not asked to record`() {
        enqueue("""{"data":{"results":{}}}""")
        val (_, url) = search(SearchScope.TOP)

        assertThat(url.encodedPath).isEqualTo("/v1/search")
        assertThat(url.queryParameter("q")).isEqualTo("cats")
        assertThat(url.queryParameter("types")).isEqualTo("posts,users,hashtags")
        assertThat(url.queryParameterNames).doesNotContain("record")
    }

    @Test
    fun `People is the users search with record=1`() {
        enqueue("""{"data":{"items":[]}}""")
        val (_, url) = search(SearchScope.PEOPLE)

        assertThat(url.encodedPath).isEqualTo("/v1/search/users")
        assertThat(url.queryParameter("q")).isEqualTo("cats")
        assertThat(url.queryParameter("record")).isEqualTo("1")
    }

    @Test
    fun `Videos and Reels are the posts search by type, not asked to record`() {
        enqueue("""{"data":{"items":[]}}""")
        val (_, videos) = search(SearchScope.VIDEOS)
        enqueue("""{"data":{"items":[]}}""")
        val (_, reels) = search(SearchScope.REELS)

        assertThat(videos.encodedPath).isEqualTo("/v1/search/posts")
        assertThat(videos.queryParameter("type")).isEqualTo("videos")
        assertThat(reels.encodedPath).isEqualTo("/v1/search/posts")
        assertThat(reels.queryParameter("type")).isEqualTo("flicks")
        assertThat(videos.queryParameterNames).doesNotContain("record")
        assertThat(reels.queryParameterNames).doesNotContain("record")
    }

    @Test
    fun `Sounds is media-service's sound search from the first offset, not asked to record`() {
        enqueue("""{"data":{"tracks":[]}}""")
        val (_, url) = search(SearchScope.SOUNDS)

        assertThat(url.encodedPath).isEqualTo("/v1/audio/search")
        assertThat(url.queryParameter("q")).isEqualTo("cats")
        assertThat(url.queryParameter("offset")).isEqualTo("0")
        assertThat(url.queryParameter("limit")).isEqualTo(SearchRepository.LIMIT.toString())
        assertThat(url.queryParameterNames).doesNotContain("record")
    }

    @Test
    fun `Hashtags is the hashtag search with record=1`() {
        enqueue("""{"data":{"hashtags":[]}}""")
        val (_, url) = search(SearchScope.HASHTAGS)

        assertThat(url.encodedPath).isEqualTo("/v1/search/hashtags")
        assertThat(url.queryParameter("record")).isEqualTo("1")
    }

    @Test
    fun `suggestions are autocomplete, never recorded`() {
        enqueue("""{"data":{"results":[{"kind":"user","user_id":"u1","username":"clee"}]}}""")
        val suggestions = runBlocking { repository.suggestions("cl") }
        val url = server.takeRequest().url()

        assertThat(url.encodedPath).isEqualTo("/v1/search/autocomplete")
        assertThat(url.queryParameterNames).doesNotContain("record")
        assertThat(suggestions).containsExactly(SearchSuggestion.Person("u1", "clee", ""))
    }

    // ── What each tab answers ──────────────────────────────────────────

    @Test
    fun `each tab answers its own kind of row`() {
        enqueue(
            """{"data":{"results":{
                 "users":{"items":[{"user_id":"u1","username":"clee","display_name":"Clee","avatar_url":null}]},
                 "hashtags":{"items":[{"hashtag":"cats","hashtag_search":"cats"}]},
                 "posts":{"items":[
                   {"id":"r1","post_id":"r1","content_type":"flick","title":"A reel"},
                   {"id":"v1","post_id":"v1","content_type":"long_video","title":"A video"},
                   {"id":"p1","post_id":"p1","content_type":"post","text":"A post"}]}}}}""",
        )
        val (top, _) = search(SearchScope.TOP)
        val hits = (top as AppResult.Success).data
        assertThat(hits.map { it::class.simpleName to it.id }).containsExactly(
            "User" to "u1",
            "Hashtag" to "cats",
            "Video" to "r1",
            "Video" to "v1",
            "Post" to "p1",
        ).inOrder()
        assertThat((hits[2] as SearchHit.Video).isReel).isTrue()
        assertThat((hits[3] as SearchHit.Video).isReel).isFalse()

        enqueue("""{"data":{"tracks":[{"id":"s1","title":"Rain","artist":"Clee","duration_ms":15000,"usage_count":12}]}}""")
        val (sounds, _) = search(SearchScope.SOUNDS)
        assertThat((sounds as AppResult.Success).data).containsExactly(
            SearchHit.Sound(id = "s1", title = "Rain", artist = "Clee", durationMs = 15_000L, usageCount = 12L, isOriginal = false),
        )

        enqueue("""{"data":{"hashtags":["#cats","Cats","kittens"]}}""")
        val (tags, _) = search(SearchScope.HASHTAGS)
        assertThat((tags as AppResult.Success).data).containsExactly(
            SearchHit.Hashtag("cats"),
            SearchHit.Hashtag("kittens"),
        ).inOrder()
    }

    @Test
    fun `a tab the server does not answer yet is an empty page, other failures are carried`() {
        enqueue("""{"error":{"code":"NOT_FOUND"}}""", code = 404)
        val (missing, _) = search(SearchScope.SOUNDS)
        assertThat(missing).isEqualTo(AppResult.Success(emptyList<SearchHit>()))

        enqueue("""{"error":{"code":"INTERNAL_ERROR"}}""", code = 500)
        val (broken, _) = search(SearchScope.PEOPLE)
        assertThat(broken).isInstanceOf(AppResult.Failure::class.java)
    }

    // ── Recents and popular ────────────────────────────────────────────

    @Test
    fun `recents are the server's history, newest first`() {
        enqueue(
            """{"data":{"items":[
                 {"id":"h2","user_id":"me","query":"dogs","searched_at":"2026-10-14T10:00:00Z"},
                 {"id":"h1","user_id":"me","query":"cats ","searched_at":"2026-10-13T10:00:00Z"},
                 {"id":"","query":"no id"}]}}""",
        )
        val recents = runBlocking { repository.history() }
        val url = server.takeRequest().url()

        assertThat(url.encodedPath).isEqualTo("/v1/search/history")
        assertThat((recents as AppResult.Success).data)
            .containsExactly(RecentSearch("h2", "dogs"), RecentSearch("h1", "cats"))
            .inOrder()
    }

    @Test
    fun `removing one recent deletes that entry only`() {
        enqueue("""{"data":{"status":"deleted"}}""")
        val result = runBlocking { repository.deleteHistoryItem("h1") }
        val request = server.takeRequest()

        assertThat(result).isEqualTo(AppResult.Success(Unit))
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.url().encodedPath).isEqualTo("/v1/search/history/h1")
    }

    @Test
    fun `clear all deletes the whole history`() {
        enqueue("""{"data":{"status":"cleared"}}""")
        val result = runBlocking { repository.clearHistory() }
        val request = server.takeRequest()

        assertThat(result).isEqualTo(AppResult.Success(Unit))
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.url().encodedPath).isEqualTo("/v1/search/history")
    }

    @Test
    fun `removing an entry that is already gone is done, but a refused clear is a failure`() {
        enqueue("""{"error":{"code":"NOT_FOUND"}}""", code = 404)
        assertThat(runBlocking { repository.deleteHistoryItem("gone") }).isEqualTo(AppResult.Success(Unit))
        server.takeRequest()

        enqueue("""{"error":{"code":"INTERNAL_ERROR"}}""", code = 500)
        assertThat(runBlocking { repository.clearHistory() }).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `popular searches parse from query rows, and a failure is none`() {
        enqueue("""{"data":{"items":[{"query":"cats"},{"query":" Cats "},{"query":""},{"query":"rain sounds"}]}}""")
        val popular = runBlocking { repository.popular() }
        val url = server.takeRequest().url()

        assertThat(url.encodedPath).isEqualTo("/v1/search/popular")
        assertThat(url.queryParameter("limit")).isEqualTo(SearchRepository.POPULAR_LIMIT.toString())
        assertThat(popular).containsExactly("cats", "rain sounds").inOrder()

        enqueue("""{"error":{"code":"INTERNAL_ERROR"}}""", code = 500)
        assertThat(runBlocking { repository.popular() }).isEmpty()
    }
}
