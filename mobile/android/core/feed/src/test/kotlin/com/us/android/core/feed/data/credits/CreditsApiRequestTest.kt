package com.us.android.core.feed.data.credits

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.PostEditApi
import com.us.android.core.feed.data.creditsPatch
import com.us.android.core.model.CreditStatus
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/** Video credits' requests, byte for byte (contract pinned 2026-10-23). */
class CreditsApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: CreditsApi
    private lateinit var edit: PostEditApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        api = retrofit.create(CreditsApi::class.java)
        edit = retrofit.create(PostEditApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun bodyOf(): JsonObject = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject

    @Test
    fun `genres are a plain GET and decode slug and label`() {
        enqueue("""{"data":[{"slug":"drama","label":"Drama"},{"slug":"sci-fi","label":"Sci-fi"}]}""")

        val genres = runBlocking { api.genres() }.data.orEmpty()

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/posts/genres")
        assertThat(genres.map { it.slug }).containsExactly("drama", "sci-fi").inOrder()
    }

    @Test
    fun `requests send no cursor on the first page and the cursor after, and read meta next_cursor`() {
        enqueue("""{"data":[{"id":"c1","role":"Actor","name":"Ravi","post":{"id":"p1"},"from":{"id":"u0"}}],"meta":{"next_cursor":"n2"}}""")
        enqueue("""{"data":[]}""")

        val first = runBlocking { api.requests() }
        runBlocking { api.requests(cursor = "n2") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/credits/requests")
        assertThat(server.takeRequest().target).isEqualTo("/v1/credits/requests?cursor=n2")
        assertThat(first.meta?.nextCursor).isEqualTo("n2")
    }

    @Test
    fun `accept and decline POST to the credit, with no body`() {
        enqueue("""{"data":{"status":"accepted"}}""")
        enqueue("""{"data":{"status":"declined"}}""")

        val accepted = runBlocking { api.accept("c1") }
        val declined = runBlocking { api.decline("c1") }

        val a = server.takeRequest()
        val d = server.takeRequest()
        assertThat(a.method).isEqualTo("POST")
        assertThat(a.target).isEqualTo("/v1/credits/c1/accept")
        assertThat(d.method).isEqualTo("POST")
        assertThat(d.target).isEqualTo("/v1/credits/c1/decline")
        assertThat(accepted.data?.status).isEqualTo("accepted")
        assertThat(declined.data?.status).isEqualTo("declined")
    }

    @Test
    fun `remove me is a DELETE of the credit that answers unlinked`() {
        enqueue("""{"data":{"status":"unlinked"}}""")

        val answer = runBlocking { api.removeMe("c1") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/credits/c1")
        assertThat(answer.data?.status).isEqualTo("unlinked")
    }

    /**
     * Under `/v1/creators`, NOT `/v1/users`: the gateway sends `/v1/users/...`
     * to user-service, which has no appearances route (server, 23 Oct).
     */
    @Test
    fun `appearances page by limit and cursor under the creator`() {
        enqueue("""{"data":[]}""")
        enqueue("""{"data":[]}""")

        runBlocking { api.appearances("u1", limit = 15) }
        runBlocking { api.appearances("u1", limit = 15, cursor = "x") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/creators/u1/appearances?limit=15")
        assertThat(server.takeRequest().target).isEqualTo("/v1/creators/u1/appearances?limit=15&cursor=x")
    }

    @Test
    fun `the PATCH sends the whole list when one credit moved, with every tag's user id`() {
        enqueue("""{"data":{"id":"p1"}}""")
        val base = CreditsForm(
            entries = listOf(
                CreditEntry("a", "Asha", "Director", CreditPerson("u1", "Asha"), CreditStatus.ACCEPTED),
                CreditEntry("b", "Ravi", "Actor", CreditPerson("u2", "Ravi"), CreditStatus.PENDING),
                CreditEntry("c", "Crew", "Editor"),
            ),
            genres = listOf("drama"),
            releaseYear = "2024",
            productionHouse = "Blue Hour",
        )
        val next = base.moved("c", -2)

        runBlocking { edit.update("p1", creditsPatch(base, next)) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.target).isEqualTo("/v1/posts/p1")
        val body = json.parseToJsonElement(request.body!!.utf8()).jsonObject
        // Only the credits moved: nothing else is on the wire.
        assertThat(body.keys).containsExactly("credits")
        val sent = body.getValue("credits").jsonArray.map { it.jsonObject }
        assertThat(sent.map { it.getValue("name").jsonPrimitive.content }).containsExactly("Crew", "Asha", "Ravi").inOrder()
        assertThat(sent.map { it["user_id"]?.jsonPrimitive?.content }).containsExactly(null, "u1", "u2").inOrder()
        assertThat(sent[0].keys).containsExactly("name", "role")
    }

    @Test
    fun `clearing sends explicit nulls for the year and the house, and an empty list for genres and credits`() {
        enqueue("""{"data":{"id":"p1"}}""")
        val base = CreditsForm(
            entries = listOf(CreditEntry("a", "Asha", "Director")),
            genres = listOf("drama"),
            releaseYear = "2024",
            productionHouse = "Blue Hour",
        )

        runBlocking { edit.update("p1", creditsPatch(base, CreditsForm())) }

        val body = bodyOf()
        assertThat(body.getValue("credits").jsonArray).isEmpty()
        assertThat(body.getValue("genres").jsonArray).isEmpty()
        assertThat(body.getValue("release_year")).isEqualTo(JsonNull)
        assertThat(body.getValue("production_house")).isEqualTo(JsonNull)
    }

    @Test
    fun `setting a year and a house sends them as values, and nothing unchanged is sent`() {
        enqueue("""{"data":{"id":"p1"}}""")
        val base = CreditsForm(genres = listOf("drama"))
        val next = base.withYear("2025").withProduction("  Blue Hour  ")

        runBlocking { edit.update("p1", creditsPatch(base, next)) }

        val body = bodyOf()
        assertThat(body.keys).containsExactly("release_year", "production_house")
        assertThat(body.getValue("release_year").jsonPrimitive.content).isEqualTo("2025")
        assertThat(body.getValue("production_house").jsonPrimitive.content).isEqualTo("Blue Hour")
    }

    @Test
    fun `nothing moved is an empty patch`() {
        val form = CreditsForm(entries = listOf(CreditEntry("a", "Asha", "Director")), genres = listOf("drama"))

        assertThat(creditsPatch(form, form).isEmpty).isTrue()
    }
}
