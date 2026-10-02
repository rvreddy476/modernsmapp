package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.network.listApiCall
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
 * The live-eligibility contract (pinned 2026-10-02) on the wire: the
 * eligibility request and its answer, the chat row's `author`, a `403
 * LIVE_NOT_ELIGIBLE` with `details.requirements`, and an emoji message sent
 * as UTF-8. The fixtures are inline and shaped like the contract's examples:
 * the backend is built in parallel, so there is no golden file to copy yet.
 * Parsing is lenient: absent, null, "" and 0 all mean "nothing".
 */
class LiveEligibilityApiRequestTest {

    private val json = NetworkModule.provideJson()
    private val errorMapper = ErrorMapper(json)
    private lateinit var server: MockWebServer
    private lateinit var api: LiveApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(LiveApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int = 200, body: String) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    // ── GET /v1/livestream/eligibility ──────────────────────────────────

    @Test
    fun `eligibility is a GET with no body and no query`() {
        enqueue(body = """{"data":{"mode":"open","eligible":true}}""")

        runBlocking { api.eligibility() }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/livestream/eligibility")
    }

    @Test
    fun `the contract's example decodes whole`() {
        enqueue(
            body = """{"data":{"mode":"open","eligible":false,
             "requirements":[{"key":"phone_verified","met":true},
              {"key":"adult","met":true},
              {"key":"account_age","met":false,"current":2,"needed":7,"unit":"days"},
              {"key":"activity","met":false,"posts":{"current":1,"needed":3},"followers":{"current":4,"needed":10}},
              {"key":"good_standing","met":true}],
             "viewer_cap":200}}""",
        )

        val answer = runBlocking { api.eligibility() }.data!!

        assertThat(answer.mode).isEqualTo("open")
        assertThat(answer.eligible).isFalse()
        assertThat(answer.pilotOnly).isFalse()
        assertThat(answer.viewerCap).isEqualTo(200)
        assertThat(answer.requirements.map { it.key })
            .containsExactly("phone_verified", "adult", "account_age", "activity", "good_standing").inOrder()
        val age = answer.requirements[2]
        assertThat(listOf(age.current, age.needed)).containsExactly(2, 7).inOrder()
        assertThat(age.unit).isEqualTo("days")
        val activity = answer.requirements[3]
        assertThat(activity.posts).isEqualTo(LiveProgressDto(current = 1, needed = 3))
        assertThat(activity.followers).isEqualTo(LiveProgressDto(current = 4, needed = 10))

        val gate = liveGateOf(answer) as LiveGate.NotYet
        assertThat(gate.action).isEqualTo(LiveGateAction.CreatePost)
    }

    @Test
    fun `met null is could-not-check, and absent fields are their empty values`() {
        enqueue(
            body = """{"data":{"mode":"open","eligible":false,"requirements":[
              {"key":"adult","met":null},
              {"key":"activity","met":false,"posts":null},
              {"key":"account_age"}],"viewer_cap":null}}""",
        )

        val answer = runBlocking { api.eligibility() }.data!!

        assertThat(answer.requirements.map { it.state }).containsExactly(
            RequirementState.Unknown,
            RequirementState.Unmet,
            RequirementState.Unknown,
        ).inOrder()
        assertThat(answer.requirements[1].posts).isNull()
        assertThat(answer.requirements[1].followers).isNull()
        assertThat(answer.requirements[2].needed).isEqualTo(0)
        assertThat(answer.requirements[2].unit).isEmpty()
        assertThat(answer.viewerCap).isEqualTo(0)
    }

    @Test
    fun `a pilot answer for a user off the list carries pilot_only`() {
        enqueue(
            body = """{"data":{"mode":"pilot","eligible":false,"pilot_only":true,""" +
                """"requirements":[{"key":"phone_verified","met":false}]}}""",
        )

        val answer = runBlocking { api.eligibility() }.data!!

        assertThat(answer.pilotOnly).isTrue()
        assertThat(liveGateOf(answer)).isEqualTo(LiveGate.PilotOnly)
    }

    @Test
    fun `null requirements and unknown keys do not break decoding`() {
        enqueue(body = """{"data":{"mode":"open","eligible":true,"requirements":null,"future":{"a":1}}}""")

        val answer = runBlocking { api.eligibility() }.data!!

        assertThat(answer.requirements).isEmpty()
        assertThat(liveGateOf(answer)).isEqualTo(LiveGate.Open())
    }

    @Test
    fun `a 403 LIVE_NOT_ELIGIBLE on create carries details requirements to the gate`() {
        enqueue(
            code = 403,
            body = """{"error":{"code":"LIVE_NOT_ELIGIBLE","message":"not yet","details":{"requirements":[""" +
                """{"key":"phone_verified","met":false},""" +
                """{"key":"activity","met":false,"posts":{"current":0,"needed":3},""" +
                """"followers":{"current":2,"needed":10}}]}}}""",
        )

        val result = runBlocking { apiCall(errorMapper) { api.createStream(CreateStreamRequest(title = "x")) } }

        val error = (result as AppResult.Failure).error
        assertThat(error).isInstanceOf(AppError.Forbidden::class.java)
        assertThat(error.liveCode()).isEqualTo(CODE_LIVE_NOT_ELIGIBLE)
        val gate = notYetFromRefusal(error, json)!!
        assertThat(gate.requirements.map { it.key }).containsExactly("phone_verified", "activity").inOrder()
        assertThat(gate.requirements[1].followers).isEqualTo(LiveProgressDto(current = 2, needed = 10))
        assertThat(gate.action).isEqualTo(LiveGateAction.VerifyPhone)
    }

    @Test
    fun `a 503 AUTHORITY_UNAVAILABLE keeps its code`() {
        enqueue(code = 503, body = """{"error":{"code":"AUTHORITY_UNAVAILABLE","message":"try later"}}""")

        val result = runBlocking { apiCall(errorMapper) { api.startStream("s1") } }

        assertThat((result as AppResult.Failure).error.liveCode()).isEqualTo(CODE_AUTHORITY_UNAVAILABLE)
    }

    // ── The chat row's author ───────────────────────────────────────────

    @Test
    fun `a chat row decodes its author whole`() {
        enqueue(
            body = """{"data":[{"id":"m1","user_id":"5f0c2a9e-1111","text":"hello 🎉","is_pinned":false,
              "created_at":"2026-10-02T10:00:00Z",
              "author":{"user_id":"5f0c2a9e-1111","name":"Asha Rao","handle":"asha",
                "avatar_url":"https://cdn.test/a.jpg","badges":["founding_creator"],"role":"host"}}]}""",
        )

        val row = (runBlocking { listApiCall(errorMapper) { api.listChat("s1") } } as AppResult.Success).data.single()

        assertThat(row.userId).isEqualTo("5f0c2a9e-1111")
        assertThat(row.text).isEqualTo("hello 🎉")
        assertThat(chatAuthorName(row.author)).isEqualTo("Asha Rao")
        assertThat(row.author?.avatarUrl).isEqualTo("https://cdn.test/a.jpg")
        assertThat(isFoundingCreator(row.author)).isTrue()
        assertThat(chatRoleOf(row, hostId = "", moderators = emptyList())).isEqualTo(ChatRole.Host)
        assertThat(server.takeRequest().target).isEqualTo("/v1/livestream/streams/s1/chat?limit=50")
    }

    @Test
    fun `only user_id and role are guaranteed - a failed lookup still names the row Viewer`() {
        enqueue(
            body = """{"data":[
              {"id":"m1","user_id":"u1","text":"a","author":{"user_id":"u1","role":"viewer"}},
              {"id":"m2","user_id":"u2","text":"b",
               "author":{"user_id":"u2","name":"","handle":"kiran","avatar_url":null,"badges":null,"role":"moderator"}},
              {"id":"m3","user_id":"u3","text":"c","author":null},
              {"id":"m4","user_id":"u4","text":"d"}]}""",
        )

        val rows = (runBlocking { listApiCall(errorMapper) { api.listChat("s1") } } as AppResult.Success).data

        assertThat(rows.map { chatAuthorName(it.author) }).containsExactly("Viewer", "@kiran", "Viewer", "Viewer")
            .inOrder()
        assertThat(rows[1].author?.badges).isEmpty()
        assertThat(rows[1].author?.avatarUrl).isEmpty()
        assertThat(chatRoleOf(rows[1], hostId = "", moderators = emptyList())).isEqualTo(ChatRole.Moderator)
        assertThat(rows[2].author).isNull()
        assertThat(rows[3].author).isNull()
        rows.forEach { row -> assertThat(chatAuthorName(row.author)).doesNotContain(row.userId) }
    }

    @Test
    fun `the send answer carries the author too`() {
        enqueue(
            body = """{"data":{"id":"m9","user_id":"me","text":"hi",""" +
                """"author":{"user_id":"me","name":"Me Myself","role":"viewer"}}}""",
        )

        val sent = runBlocking { api.sendChat("s1", SendChatRequest(text = "hi")) }.data!!

        assertThat(chatAuthorName(sent.author)).isEqualTo("Me Myself")
    }

    @Test
    fun `an emoji message leaves the phone as UTF-8 JSON, unescaped and whole`() {
        enqueue(body = """{"data":{"id":"m9","user_id":"me","text":"x"}}""")
        val text = "nice 🎉 👩‍👩‍👧 ❤️"

        runBlocking { api.sendChat("s1", SendChatRequest(text = text)) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/livestream/streams/s1/chat")
        assertThat(request.headers["Content-Type"]).contains("application/json")
        val body = request.body!!
        assertThat(body.utf8()).isEqualTo("""{"text":"$text"}""")
        // Four bytes per astral emoji on the wire: UTF-8, not a lossy charset.
        assertThat(body.size).isEqualTo("""{"text":"$text"}""".toByteArray(Charsets.UTF_8).size)
    }
}
