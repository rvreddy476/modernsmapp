package com.us.android.core.feed.ui.action

import com.google.common.truth.Truth.assertThat
import com.us.android.core.datastore.LeavingDomainsStore
import com.us.android.core.feed.data.action.ActionApi
import com.us.android.core.feed.data.action.ActionLinkDto
import com.us.android.core.feed.data.action.ActionLinkResult
import com.us.android.core.feed.data.action.ActionRepository
import com.us.android.core.feed.data.action.ActionStatsDto
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionLinks
import com.us.android.core.model.ActionStatus
import com.us.android.core.model.PostAction
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import retrofit2.HttpException
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * A tap on an Action button, through the real Retrofit stack: the links
 * route is asked first (it counts the click), a gone link says so, the
 * leaving page shows the first time a domain is opened and never again
 * after Continue, and a button that is not live is never opened.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ActionLinkTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: ActionRepository

    /** The DataStore stand-in, through the same pure rules. */
    private class MemoryStore : LeavingDomainsStore {
        var domains: List<String> = emptyList()
        override suspend fun seen(): List<String> = domains
        override suspend fun remember(domain: String) {
            domains = ActionLinks.remember(domains, domain)
        }
    }

    private val store = MemoryStore()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ActionApi::class.java)
        repository = ActionRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private val button = PostAction("a1", "apply_now", "Apply now", "epfindia.gov.in", "/l/a1")
    private val link = """{"data":{"id":"a1","url":"https://www.epfindia.gov.in/apply","domain":"epfindia.gov.in","label":"apply_now","status":"active"}}"""

    // ---- the repository ----

    @Test
    fun `labels read a bare array or the envelope, and fall back to the contract's list`() {
        enqueue("""[{"value":"apply_now","label":"Apply now"},{"value":"book_now","label":"Book"}]""")
        assertThat(runBlocking { repository.labels() }.map { it.label }).containsExactly("Apply now", "Book").inOrder()
        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/action-labels")
        // Cached for the process once read: no second request.
        assertThat(runBlocking { repository.labels() }.map { it.label }).containsExactly("Apply now", "Book").inOrder()
        assertThat(server.requestCount).isEqualTo(1)

        val fresh = ActionRepository(
            Retrofit.Builder().baseUrl(server.url("/"))
                .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
                .build().create(ActionApi::class.java),
            ErrorMapper(json),
        )
        enqueue("""{"data":[{"value":"shop_now","label":"Shop now"}]}""")
        assertThat(runBlocking { fresh.labels() }.map { it.value }).containsExactly("shop_now")
        val failing = ActionRepository(
            Retrofit.Builder().baseUrl(server.url("/"))
                .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
                .build().create(ActionApi::class.java),
            ErrorMapper(json),
        )
        enqueue("""{"error":{"code":"INTERNAL"}}""", code = 500)
        assertThat(runBlocking { failing.labels() }).isEqualTo(ActionButtons.LABELS)
    }

    @Test
    fun `the link is read by id and named by the url's real host`() {
        enqueue(link)
        val result = runBlocking { repository.link("a1") } as ActionLinkResult.Ready
        assertThat(server.takeRequest().target).isEqualTo("/v1/links/a1")
        assertThat(result.target.url).isEqualTo("https://www.epfindia.gov.in/apply")
        assertThat(result.target.domain).isEqualTo("epfindia.gov.in")
    }

    @Test
    fun `a 404, a 410, a link that is not active or not https is gone`() {
        enqueue("""{"error":{"code":"NOT_FOUND"}}""", code = 404)
        assertThat(runBlocking { repository.link("a1") }).isEqualTo(ActionLinkResult.Gone)
        enqueue("""{"error":{"code":"GONE"}}""", code = 410)
        assertThat(runBlocking { repository.link("a1") }).isEqualTo(ActionLinkResult.Gone)
        enqueue("""{"data":{"id":"a1","url":"https://x.in","status":"blocked"}}""")
        assertThat(runBlocking { repository.link("a1") }).isEqualTo(ActionLinkResult.Gone)
        enqueue("""{"data":{"id":"a1","url":"javascript:alert(1)","status":"active"}}""")
        assertThat(runBlocking { repository.link("a1") }).isEqualTo(ActionLinkResult.Gone)
        enqueue("""{"error":{"code":"INTERNAL"}}""", code = 500)
        assertThat(runBlocking { repository.link("a1") }).isInstanceOf(ActionLinkResult.Failed::class.java)
        assertThat(runBlocking { repository.link(" ") }).isEqualTo(ActionLinkResult.Gone)
    }

    @Test
    fun `stats read the author's taps, and nothing when they cannot be read`() {
        enqueue("""{"data":{"clicks_total":12,"clicks_7d":3}}""")
        val stats = runBlocking { repository.stats("p1") }!!
        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/p1/action/stats")
        assertThat(stats.tapsText).isEqualTo("12 taps · 3 this week")
        enqueue("""{"error":{"code":"FORBIDDEN"}}""", code = 403)
        assertThat(runBlocking { repository.stats("p1") }).isNull()
        // The profile button's taps: the owner's channel.
        enqueue("""{"data":{"clicks_total":1,"clicks_7d":0}}""")
        assertThat(runBlocking { repository.profileStats() }!!.tapsText).isEqualTo("1 tap")
        server.takeRequest()
        assertThat(server.takeRequest().target).isEqualTo("/v1/channels/me/action/stats")
    }

    // ---- the opener (a fake API: no real IO under the test dispatcher) ----

    private class FakeActionApi : ActionApi {
        val answers = ArrayDeque<() -> ApiEnvelope<ActionLinkDto>>()
        var calls = 0
        override suspend fun labels(): JsonElement = JsonArray(emptyList())
        override suspend fun link(linkId: String): ApiEnvelope<ActionLinkDto> {
            calls++
            return answers.removeFirst().invoke()
        }
        override suspend fun stats(postId: String): ApiEnvelope<ActionStatsDto> = ApiEnvelope(data = ActionStatsDto())
        override suspend fun profileStats(): ApiEnvelope<ActionStatsDto> = ApiEnvelope(data = ActionStatsDto())
    }

    private val fake = FakeActionApi()
    private val live = ActionLinkDto("a1", "https://www.epfindia.gov.in/apply", "epfindia.gov.in", "apply_now", "active")

    private fun answer(dto: ActionLinkDto = live) = fake.answers.addLast { ApiEnvelope(data = dto) }

    private fun fail(code: Int) = fake.answers.addLast {
        throw HttpException(Response.error<Any>(code, """{"error":{"code":"X"}}""".toResponseBody("application/json".toMediaType())))
    }

    private fun opener() = ActionLinkViewModel(ActionRepository(fake, ErrorMapper(json)), store)

    @Test
    fun `the first tap on a domain shows the leaving page, Continue remembers it and opens`() = runTest(dispatcher) {
        val model = opener()
        answer()
        model.open("pill-1", button)
        advanceUntilIdle()
        val leaving = model.state.value as ActionLinkState.Leaving
        assertThat(leaving.owner).isEqualTo("pill-1")
        assertThat(leaving.target.domain).isEqualTo("epfindia.gov.in")
        assertThat(model.launch.value).isNull()

        model.continueToSite()
        advanceUntilIdle()
        assertThat(store.domains).containsExactly("epfindia.gov.in")
        assertThat(model.launch.value).isEqualTo("pill-1" to "https://www.epfindia.gov.in/apply")
        assertThat(model.state.value).isEqualTo(ActionLinkState.Idle)
        model.launched()
        assertThat(model.launch.value).isNull()

        // The same domain again: straight to the browser, but still through the links route (the click counts).
        answer()
        model.open("pill-2", button)
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(ActionLinkState.Idle)
        assertThat(model.launch.value).isEqualTo("pill-2" to "https://www.epfindia.gov.in/apply")
        assertThat(fake.calls).isEqualTo(2)
    }

    @Test
    fun `Go back opens nothing and remembers nothing`() = runTest(dispatcher) {
        val model = opener()
        answer()
        model.open("pill-1", button)
        advanceUntilIdle()
        model.dismiss()
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(ActionLinkState.Idle)
        assertThat(model.launch.value).isNull()
        assertThat(store.domains).isEmpty()
    }

    @Test
    fun `a gone link says so, and a failure can be tried again`() = runTest(dispatcher) {
        val model = opener()
        fail(404)
        model.open("pill-1", button)
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(ActionLinkState.Gone("pill-1"))

        fail(500)
        model.open("pill-1", button)
        advanceUntilIdle()
        assertThat(model.state.value).isInstanceOf(ActionLinkState.Failed::class.java)
        answer()
        model.retry()
        advanceUntilIdle()
        assertThat(model.state.value).isInstanceOf(ActionLinkState.Leaving::class.java)
    }

    @Test
    fun `a button that is not live is never opened`() = runTest(dispatcher) {
        val model = opener()
        model.open("pill-1", button.copy(status = ActionStatus.BLOCKED))
        model.open("pill-1", button.copy(status = ActionStatus.UNDER_REVIEW))
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(ActionLinkState.Idle)
        assertThat(fake.calls).isEqualTo(0)
    }
}
