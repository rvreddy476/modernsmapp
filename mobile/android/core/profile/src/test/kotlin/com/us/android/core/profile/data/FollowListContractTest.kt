package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.jsonObject
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
 * The follower / following lists: the graph route's two answer shapes, the
 * query each page sends, and the two batch bodies that hydrate a page. The
 * hydration requests go out together, so the server here answers by path
 * rather than by queue order.
 */
class FollowListContractTest {

    /** NetworkModule.provideJson. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: FollowListRepository
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private val answers = mutableMapOf<String, Pair<Int, String>>()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                val (code, body) = answers[request.url.encodedPath] ?: (404 to "")
                return MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build()
            }
        }
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        repository = FollowListRepository(retrofit.create(FollowListApi::class.java), ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun request(path: String) = requests.single { it.url.encodedPath == path }

    private fun bodyOf(path: String) = json.parseToJsonElement(request(path).body!!.utf8()).jsonObject

    private fun answerHydration() {
        answers["/v1/profiles/batch"] = 200 to
            """{"a":{"user_id":"a","username":"ana","display_name":"Ana","avatar_media_id":"m-a"},""" +
            """"b":{"user_id":"b","username":"ben","display_name":"Ben","avatar_media_id":null},""" +
            """"me":{"user_id":"me","username":"self","display_name":"Me"}}"""
        answers["/v1/graph/relationships/batch"] = 200 to
            """{"a":{"follows":true,"follow_request_status":"none"},""" +
            """"b":{"follows":false,"follow_request_status":"pending_sent"}}"""
    }

    @Test
    fun `the first page asks for cursor mode and hydrates names and the viewer's edges`() = runTest {
        answers["/v1/graph/followers/u1"] = 200 to
            """{"data":{"items":["a","b","me"],"next_cursor":"c-2"}}"""
        answerHydration()

        val result = repository.page(FollowListKind.FOLLOWERS, userId = "u1", viewerId = "me")

        val list = request("/v1/graph/followers/u1")
        assertThat(list.method).isEqualTo("GET")
        assertThat(list.url.queryParameter("limit")).isEqualTo("20")
        assertThat(list.url.queryParameter("paginate")).isEqualTo("cursor")
        assertThat(list.url.queryParameter("cursor")).isNull()

        assertThat(request("/v1/profiles/batch").method).isEqualTo("POST")
        assertThat(bodyOf("/v1/profiles/batch")).isEqualTo(
            json.parseToJsonElement("""{"user_ids":["a","b","me"]}"""),
        )
        // The viewer is never asked about themself.
        assertThat(bodyOf("/v1/graph/relationships/batch")).isEqualTo(
            json.parseToJsonElement("""{"viewer_id":"me","target_ids":["a","b"]}"""),
        )

        val page = (result as AppResult.Success).data
        assertThat(page.nextCursor).isEqualTo("c-2")
        assertThat(page.items.map { it.userId }).containsExactly("a", "b", "me").inOrder()
        assertThat(page.items[0]).isEqualTo(
            FollowListPerson("a", "ana", "Ana", "m-a", isVerified = false, viewerEdge = FollowStatus.FOLLOWING),
        )
        assertThat(page.items[1].viewerEdge).isEqualTo(FollowStatus.REQUESTED)
        assertThat(page.items[1].avatarMediaId).isNull()
    }

    @Test
    fun `a later page sends the cursor and not the mode`() = runTest {
        answers["/v1/graph/following/u1"] = 200 to """{"data":{"items":["a"],"next_cursor":""}}"""
        answerHydration()

        val result = repository.page(FollowListKind.FOLLOWING, userId = "u1", viewerId = "me", cursor = "c-2")

        val list = request("/v1/graph/following/u1")
        assertThat(list.url.queryParameter("cursor")).isEqualTo("c-2")
        assertThat(list.url.queryParameter("paginate")).isNull()
        // A blank next cursor is the last page.
        assertThat((result as AppResult.Success).data.nextCursor).isNull()
    }

    @Test
    fun `a blocked caller's bare empty array is an empty last page with no hydration`() = runTest {
        answers["/v1/graph/followers/u1"] = 200 to """{"data":[]}"""

        val result = repository.page(FollowListKind.FOLLOWERS, userId = "u1", viewerId = "me")

        val page = (result as AppResult.Success).data
        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
        assertThat(requests.map { it.url.encodedPath }).containsExactly("/v1/graph/followers/u1")
    }

    @Test
    fun `a failed relationship read keeps the rows with no edge`() = runTest {
        answers["/v1/graph/followers/u1"] = 200 to """{"data":{"items":["a"],"next_cursor":""}}"""
        answerHydration()
        answers["/v1/graph/relationships/batch"] = 500 to """{"error":{"code":"INTERNAL_ERROR"}}"""

        val result = repository.page(FollowListKind.FOLLOWERS, userId = "u1", viewerId = "me")

        assertThat((result as AppResult.Success).data.items.single().viewerEdge).isNull()
    }

    @Test
    fun `a failed profile read fails the page`() = runTest {
        answers["/v1/graph/followers/u1"] = 200 to """{"data":{"items":["a"],"next_cursor":""}}"""
        answers["/v1/graph/relationships/batch"] = 200 to """{}"""
        answers["/v1/profiles/batch"] = 500 to """{"error":{"code":"INTERNAL_ERROR"}}"""

        val result = repository.page(FollowListKind.FOLLOWERS, userId = "u1", viewerId = "me")

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `a person the profiles batch left out is left out of the page`() = runTest {
        answers["/v1/graph/followers/u1"] = 200 to """{"data":{"items":["a","gone"],"next_cursor":""}}"""
        answerHydration()

        val result = repository.page(FollowListKind.FOLLOWERS, userId = "u1", viewerId = "me")

        assertThat((result as AppResult.Success).data.items.map { it.userId }).containsExactly("a")
    }

    @Test
    fun `signed out, no relationship read is made`() = runTest {
        answers["/v1/graph/followers/u1"] = 200 to """{"data":{"items":["a"],"next_cursor":""}}"""
        answerHydration()

        val result = repository.page(FollowListKind.FOLLOWERS, userId = "u1", viewerId = "")

        assertThat(requests.none { it.url.encodedPath == "/v1/graph/relationships/batch" }).isTrue()
        assertThat((result as AppResult.Success).data.items.single().viewerEdge).isNull()
    }

    @Test
    fun `every list shape reads to ids`() {
        assertThat(idsFromGraphList(json.parseToJsonElement("""["a","b","a"]""")).items).containsExactly("a", "b")
        assertThat(idsFromGraphList(JsonNull).items).isEmpty()
        assertThat(idsFromGraphList(null).items).isEmpty()
        val cursorPage = idsFromGraphList(json.parseToJsonElement("""{"items":["x"],"next_cursor":"n"}"""))
        assertThat(cursorPage.items).containsExactly("x")
        assertThat(cursorPage.nextCursor).isEqualTo("n")
        assertThat(idsFromGraphList(json.parseToJsonElement("""{"items":null}""")).items).isEmpty()
    }
}
