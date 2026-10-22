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
 * Settings › Privacy › Blocked accounts (2026-10-22) against graph-service's
 * handlers: `GetBlockedAndMuted` (viewer-bound, `{"data":{"user_ids":[…]}}`,
 * blocks both ways plus the viewer's mutes), `GetRelationshipBatch` (a bare
 * map; `blocked` = the viewer blocked the target), `Unblock` (DELETE with a
 * body, `{"data":{"status":"unblocked"}}`) and `Unmute` (DELETE with a body,
 * 204), with profile-service's bare profiles batch for the names.
 */
class BlockedAccountsApiRequestTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: BlockedAccountsRepository
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
        repository = BlockedAccountsRepository(
            api = retrofit.create(BlockedAccountsApi::class.java),
            profiles = retrofit.create(FollowListApi::class.java),
            profileApi = retrofit.create(ProfileApi::class.java),
            errorMapper = ErrorMapper(json),
        )
    }

    @After
    fun tearDown() = server.close()

    private fun request(line: String) = requests.single { "${it.method} ${it.url.encodedPath}" == line }

    private fun answerList() {
        // a: the viewer blocked them. b: blocked the viewer (not theirs to undo). c: muted only.
        answers["GET /v1/graph/blocked-and-muted"] = 200 to """{"data":{"user_ids":["a","b","c","me"]}}"""
        answers["POST /v1/graph/relationships/batch"] = 200 to
            """{"a":{"blocked":true,"viewer_blocked_target":true,"is_muted":false},""" +
            """"b":{"blocked":false,"blocked_by":true,"viewer_blocked_target":false,"target_blocked_viewer":true},""" +
            """"c":{"blocked":false,"viewer_blocked_target":false,"is_muted":true}}"""
        answers["POST /v1/profiles/batch"] = 200 to
            """{"a":{"user_id":"a","username":"ana","display_name":"Ana","avatar_media_id":"m-a"},""" +
            """"c":{"user_id":"c","username":"cy","display_name":"Cy"}}"""
    }

    @Test
    fun `the list is the viewer's own blocks and mutes, never a block made against them`() {
        answerList()

        val result = runBlocking { repository.load(viewerId = "me") }

        assertThat(request("GET /v1/graph/blocked-and-muted").url.query).isNull()
        // The viewer is named and never asked about; the other three go in one batch.
        assertThat(request("POST /v1/graph/relationships/batch").body!!.utf8())
            .isEqualTo("""{"viewer_id":"me","target_ids":["a","b","c"]}""")
        assertThat(request("POST /v1/profiles/batch").body!!.utf8()).isEqualTo("""{"user_ids":["a","c"]}""")
        val accounts = (result as AppResult.Success).data
        assertThat(accounts.blocked.map { it.userId }).containsExactly("a")
        assertThat(accounts.blocked.single().displayName).isEqualTo("Ana")
        assertThat(accounts.blocked.single().avatarMediaId).isEqualTo("m-a")
        assertThat(accounts.muted.map { it.userId }).containsExactly("c")
    }

    /** A server without the explicit fields still answers through the legacy `blocked`. */
    @Test
    fun `the legacy blocked field is read when the explicit one is absent`() {
        answers["GET /v1/graph/blocked-and-muted"] = 200 to """{"data":{"user_ids":["a"]}}"""
        answers["POST /v1/graph/relationships/batch"] = 200 to """{"a":{"blocked":true}}"""
        answers["POST /v1/profiles/batch"] = 200 to """{}"""

        val accounts = (runBlocking { repository.load("me") } as AppResult.Success).data

        assertThat(accounts.blocked.map { it.userId }).containsExactly("a")
        // Not in the profiles batch: still listed, nameless.
        assertThat(accounts.blocked.single().displayName).isEmpty()
    }

    @Test
    fun `nobody blocked is two empty lists and no batch`() {
        answers["GET /v1/graph/blocked-and-muted"] = 200 to """{"data":{"user_ids":[]}}"""

        val accounts = (runBlocking { repository.load("me") } as AppResult.Success).data

        assertThat(accounts.blocked).isEmpty()
        assertThat(accounts.muted).isEmpty()
        assertThat(requests.map { it.url.encodedPath }).containsExactly("/v1/graph/blocked-and-muted")
    }

    /** Without the batch a block cannot be told from a mute, or from a block made against the viewer. */
    @Test
    fun `a failed relationships batch fails the page`() {
        answers["GET /v1/graph/blocked-and-muted"] = 200 to """{"data":{"user_ids":["a"]}}"""

        val result = runBlocking { repository.load("me") }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `a refused list is a failure`() {
        answers["GET /v1/graph/blocked-and-muted"] = 400 to """{"error":{"code":"VIEWER_BOUND","message":"no"}}"""

        assertThat(runBlocking { repository.load("me") }).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `unblock is the DELETE on block with the user in the body`() {
        answers["DELETE /v1/graph/block"] = 200 to """{"data":{"status":"unblocked"}}"""

        val result = runBlocking { repository.unblock("a") }

        assertThat(request("DELETE /v1/graph/block").body!!.utf8()).isEqualTo("""{"user_id":"a"}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `unmute is the DELETE on mute with the user in the body, and 204 is success`() {
        answers["DELETE /v1/graph/mute"] = 204 to ""

        val result = runBlocking { repository.unmute("c") }

        assertThat(request("DELETE /v1/graph/mute").body!!.utf8()).isEqualTo("""{"user_id":"c"}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `a refused unblock is a failure`() {
        answers["DELETE /v1/graph/block"] = 429 to """{"error":{"code":"RATE_LIMITED","message":"slow down"}}"""

        assertThat(runBlocking { repository.unblock("a") }).isInstanceOf(AppResult.Failure::class.java)
    }
}
