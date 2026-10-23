package com.us.android.core.feed.data.credits

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.PostEditApi
import com.us.android.core.feed.data.PostEditDetailDto
import com.us.android.core.feed.data.creditsForm
import com.us.android.core.feed.data.creditsPatch
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.postEditRefusal
import com.us.android.core.feed.data.serverCode
import com.us.android.core.feed.data.toDomain
import com.us.android.core.model.CreditStatus
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
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
import java.io.File

/**
 * Video credits against post-service's golden files
 * (`internal/http/testdata/contracts/credits`), copied byte for byte into
 * `src/test/resources/contracts/credits`. When the app and a fixture
 * disagree, the fixture wins.
 *
 * Every shape the app claims WHOLE — a credit row, its user, a genre, a
 * credit request, an answer's `{status}`, the envelope's `meta` and `error`
 * — decodes STRICTLY, so a key the server adds or renames fails here rather
 * than defaulting silently in production. A post body is read with the
 * platform `Json`: `FeedItemDto` reads a post, it does not claim every key.
 *
 * The request fixtures are the bodies the app must SEND: the PATCH the
 * owner's editor builds equals `patch_request` and `patch_clear_request` as
 * JSON trees, read the way the server reads them (see [asServerReadsPatch]).
 */
class CreditsContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val app = NetworkModule.provideJson()
    private val dir = File("src/test/resources/contracts/credits")

    private lateinit var server: MockWebServer
    private lateinit var api: CreditsApi
    private lateinit var edit: PostEditApi
    private lateinit var mapper: ErrorMapper
    private lateinit var repository: CreditsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(app.asConverterFactory("application/json".toMediaType()))
            .build()
        api = retrofit.create(CreditsApi::class.java)
        edit = retrofit.create(PostEditApi::class.java)
        mapper = ErrorMapper(app)
        repository = CreditsRepository(api, mapper, FeedItemHydrator { it })
    }

    @After
    fun tearDown() = server.close()

    private fun read(name: String): String = File(dir, name).readText()

    private fun envelope(name: String): JsonObject = app.parseToJsonElement(read(name)).jsonObject

    private fun data(name: String): JsonElement = envelope(name).getValue("data")

    private fun serve(name: String, code: Int = 200) {
        server.enqueue(
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(read(name)).build(),
        )
    }

    // ── Every fixture is claimed ────────────────────────────────────────

    @Test
    fun `every credits fixture copied here is read by a test here`() {
        assertThat(dir.list().orEmpty().toSet()).containsExactlyElementsIn(CLAIMED)
    }

    // ── Reads ───────────────────────────────────────────────────────────

    @Test
    fun `the owner's post detail - every status, every tag's user id, an account only when accepted`() {
        val credits = data("post_detail_owner.json").jsonObject.getValue("credits").jsonArray
        credits.forEach { strict.decodeFromJsonElement(CreditDto.serializer(), it) }

        val item = app.decodeFromString(ApiEnvelope.serializer(FeedItemDto.serializer()), read("post_detail_owner.json"))
            .data!!.toDomain()

        val people = item.credits.credits
        assertThat(people.map { it.name })
            .containsExactly("Asha Rao", "Ravi Kumar", "Dev Menon", "Meera Iyer", "Kiran (me)").inOrder()
        assertThat(people.map { it.status }).containsExactly(
            CreditStatus.ACCEPTED,
            CreditStatus.PENDING,
            CreditStatus.DECLINED,
            CreditStatus.UNLINKED,
            CreditStatus.ACCEPTED,
        ).inOrder()
        assertThat(people.map { it.linkedUserId }).containsExactly(ASHA, RAVI, DEV, null, OWNER).inOrder()
        assertThat(people.map { it.user?.username }).containsExactly("asha", null, null, null, "kiran").inOrder()
        assertThat(people[0].user?.avatarUrl).isEqualTo("/v1/media/d8888888-8888-4888-8888-888888888888/serve/avatar")
        assertThat(people[4].user?.avatarUrl).isNull()
        assertThat(item.credits.genres).containsExactly("drama", "romance").inOrder()
        assertThat(item.credits.releaseYear).isEqualTo(2024)
        assertThat(item.credits.productionHouse).isEqualTo("Blue Hour Films")
    }

    @Test
    fun `a viewer's post detail - plain names, no status, no user_id, an account only on the accepted credits`() {
        val credits = data("post_detail_viewer.json").jsonObject.getValue("credits").jsonArray.map { it.jsonObject }
        credits.forEach { strict.decodeFromJsonElement(CreditDto.serializer(), it) }
        credits.forEach { assertThat(it.keys).containsNoneOf("status", "user_id") }

        val people = app.decodeFromString(ApiEnvelope.serializer(FeedItemDto.serializer()), read("post_detail_viewer.json"))
            .data!!.toDomain().credits.credits

        assertThat(people.map { it.status }).containsExactly(null, null, null, null, null)
        assertThat(people.map { it.user?.id }).containsExactly(ASHA, null, null, null, OWNER).inOrder()
        // The pending and declined tags are invisible to a viewer: no link of any kind.
        assertThat(people.subList(1, 4).map { it.linkedUserId }).containsExactly(null, null, null)
    }

    @Test
    fun `the owner's edit form keeps every tag's user id, and an unchanged save sends nothing`() {
        val detail = app.decodeFromString(ApiEnvelope.serializer(PostEditDetailDto.serializer()), read("post_detail_owner.json")).data!!
        val form = detail.creditsForm { "k$it" }

        assertThat(form.entries.map { it.person?.id }).containsExactly(ASHA, RAVI, DEV, null, OWNER).inOrder()
        assertThat(form.toInput().map { it.userId }).containsExactly(ASHA, RAVI, DEV, null, OWNER).inOrder()
        assertThat(form.entries.map { it.status }).containsExactly(
            CreditStatus.ACCEPTED,
            CreditStatus.PENDING,
            CreditStatus.DECLINED,
            CreditStatus.UNLINKED,
            CreditStatus.ACCEPTED,
        ).inOrder()
        assertThat(form.genres).containsExactly("drama", "romance").inOrder()
        assertThat(form.releaseYear).isEqualTo("2024")
        assertThat(form.productionHouse).isEqualTo("Blue Hour Films")
        assertThat(creditsPatch(form, form).isEmpty).isTrue()
    }

    @Test
    fun `the create answer carries the owner's credits and meta credit_tags_skipped`() {
        val root = envelope("create_response.json")
        root.getValue("data").jsonObject.getValue("credits").jsonArray
            .forEach { strict.decodeFromJsonElement(CreditDto.serializer(), it) }

        val meta = strict.decodeFromJsonElement(ApiMeta.serializer(), root.getValue("meta"))
        assertThat(meta.creditTagsSkipped).isEqualTo(1)

        val people = app.decodeFromString(ApiEnvelope.serializer(FeedItemDto.serializer()), read("create_response.json"))
            .data!!.toDomain().credits.credits
        assertThat(people.map { it.status }).containsExactly(
            CreditStatus.ACCEPTED,
            CreditStatus.PENDING,
            CreditStatus.DECLINED,
            CreditStatus.UNLINKED,
        ).inOrder()
    }

    @Test
    fun `meta without credit_tags_skipped - it is sent only when more than zero - reads as none`() {
        val meta = strict.decodeFromJsonElement(ApiMeta.serializer(), envelope("requests.json").getValue("meta"))

        assertThat(meta.creditTagsSkipped).isNull()
    }

    /** Appears in rows are channel-video rows: genres, year and house, but never `credits`. */
    @Test
    fun `appearances are read from the creators route, and their rows carry no credits`() = runTest {
        val row = data("appearances.json").jsonArray.single().jsonObject
        assertThat(row.keys).doesNotContain("credits")
        assertThat(row.keys).containsAtLeast("genres", "release_year", "production_house", "channel")
        serve("appearances.json")
        // The fixture has a next cursor, so the pager prefetches a second page: the end.
        server.enqueue(MockResponse.Builder().code(200).body("""{"data":[]}""").build())

        val items = repository.appearances(OWNER).asSnapshot()

        assertThat(server.takeRequest().url.encodedPath).isEqualTo("/v1/creators/$OWNER/appearances")
        val item = items.single()
        assertThat(item.id).isEqualTo("d1111111-1111-4111-8111-111111111111")
        assertThat(item.credits.credits).isEmpty()
        assertThat(item.credits.genres).containsExactly("drama", "romance").inOrder()
        assertThat(item.credits.releaseYear).isEqualTo(2024)
        assertThat(item.channel?.handle).isEqualTo("bluehour")
    }

    @Test
    fun `the appearances page carries meta next_cursor`() {
        val page = app.decodeFromString(ApiEnvelope.serializer(ListSerializer(FeedItemDto.serializer())), read("appearances.json"))

        assertThat(page.meta?.nextCursor).isEqualTo(
            "MjAyNi0xMC0yM1QwOTozMDowMFp8ZDExMTExMTEtMTExMS00MTExLTgxMTEtMTExMTExMTExMTEx",
        )
    }

    @Test
    fun `the genre list decodes strictly, slug and label`() = runBlocking<Unit> {
        val strictly = strict.decodeFromString(ApiEnvelope.serializer(ListSerializer(GenreDto.serializer())), read("genres.json"))
        assertThat(strictly.data).hasSize(20)
        serve("genres.json")

        val genres = repository.genres().orEmpty()

        assertThat(genres.map { it.slug }).containsAtLeast("action", "sci-fi", "news").inOrder()
        assertThat(genres.first { it.slug == "sci-fi" }.label).isEqualTo("Sci-fi")
    }

    @Test
    fun `credit requests decode strictly, with the post and who sent it, and the next cursor`() = runBlocking<Unit> {
        strict.decodeFromString(ApiEnvelope.serializer(ListSerializer(CreditRequestDto.serializer())), read("requests.json"))
        serve("requests.json")

        val page = (repository.requests() as AppResult.Success).data

        val request = page.items.single()
        assertThat(request.id).isEqualTo(RAVI_CREDIT)
        assertThat(request.role).isEqualTo("Actor")
        assertThat(request.name).isEqualTo("Ravi Kumar")
        assertThat(request.postId).isEqualTo("d1111111-1111-4111-8111-111111111111")
        assertThat(request.postTitle).isEqualTo("Monsoon Letters")
        assertThat(request.coverUrl).isEqualTo("/v1/media/d7777777-7777-4777-8777-777777777777/serve")
        assertThat(request.channelName).isEqualTo("Blue Hour Films")
        assertThat(request.from.displayName).isEqualTo("Kiran")
        assertThat(request.from.username).isEqualTo("kiran")
        assertThat(page.nextCursor).isEqualTo("MjAyNi0xMC0yM1QwOTozMDowMFp8ZTIyMjIyMjItMjIyMi00MjIyLTgyMjItMjIyMjIyMjIyMjIy")
    }

    @Test
    fun `accept, decline and remove answer their status, strictly, and each is a success`() = runBlocking<Unit> {
        mapOf("accept_response.json" to "accepted", "decline_response.json" to "declined", "remove_response.json" to "unlinked")
            .forEach { (name, status) ->
                assertThat(strict.decodeFromString(ApiEnvelope.serializer(CreditStatusDto.serializer()), read(name)).data?.status)
                    .isEqualTo(status)
            }
        serve("accept_response.json")
        serve("decline_response.json")
        serve("remove_response.json")

        assertThat(repository.accept(RAVI_CREDIT)).isEqualTo(AppResult.Success(Unit))
        assertThat(repository.decline(RAVI_CREDIT)).isEqualTo(AppResult.Success(Unit))
        assertThat(repository.removeMe(RAVI_CREDIT)).isEqualTo(AppResult.Success(Unit))
        assertThat(server.takeRequest().target).isEqualTo("/v1/credits/$RAVI_CREDIT/accept")
        assertThat(server.takeRequest().target).isEqualTo("/v1/credits/$RAVI_CREDIT/decline")
        assertThat(server.takeRequest().method).isEqualTo("DELETE")
    }

    /** A second DELETE is 404: the viewer is not linked any more, which is what Remove asked for. */
    @Test
    fun `a repeated remove answers 404, and that is still a success, while accept on a gone credit is NotFound`() = runBlocking<Unit> {
        strict.decodeFromString(ApiEnvelope.serializer(JsonObject.serializer()), read("error_credit_not_found.json"))
        serve("error_credit_not_found.json", code = 404)
        serve("error_credit_not_found.json", code = 404)

        assertThat(repository.removeMe(RAVI_CREDIT)).isEqualTo(AppResult.Success(Unit))
        val accepted = repository.accept(RAVI_CREDIT)
        assertThat((accepted as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
    }

    // ── Refusals ────────────────────────────────────────────────────────

    @Test
    fun `the credits refusals are said in plain words, never the code or the server's message`() = runBlocking<Unit> {
        listOf("error_credits_not_allowed.json", "error_invalid_genre.json").forEach { name ->
            val body = strict.decodeFromString(ApiEnvelope.serializer(JsonObject.serializer()), read(name)).error!!
            serve(name, code = 400)

            val error = (apiCall(mapper) { edit.update("p1", creditsPatch(CreditsForm(), CreditsForm(genres = listOf("x")))) }
                as AppResult.Failure).error
            val said = creditRefusal(error.serverCode())

            assertThat(error.serverCode()).isEqualTo(body.code)
            assertThat(said).isNotNull()
            assertThat(said).doesNotContain(body.code)
            assertThat(said).isNotEqualTo(body.message)
            assertThat(postEditRefusal(error)).isEqualTo(said)
        }
    }

    @Test
    fun `every credits 400 the server has has its own sentence`() {
        val codes = listOf(
            "CREDITS_NOT_ALLOWED",
            "INVALID_GENRE",
            "TOO_MANY_CREDITS",
            "INVALID_CREDIT",
            "INVALID_RELEASE_YEAR",
            "INVALID_PRODUCTION_HOUSE",
        )

        assertThat(CreditRefusals.keys).containsExactlyElementsIn(codes)
        codes.forEach { code ->
            val said = creditRefusal(code)!!
            assertThat(said).doesNotContain("_")
            assertThat(said).doesNotContain(code)
        }
        assertThat(creditRefusal("TITLE_TOO_LONG")).isNull()
        assertThat(creditRefusal(null)).isNull()
    }

    // ── Requests the app sends ──────────────────────────────────────────

    /** The form `patch_request.json` describes: two credits (one linked), two genres, a year and a house. */
    private val patchForm = CreditsForm(
        entries = listOf(
            CreditEntry("a", "Asha Rao", "Director", CreditPerson(ASHA, "Asha Rao")),
            CreditEntry("b", "Meera Iyer", "Music"),
        ),
        genres = listOf("drama", "romance"),
        releaseYear = "2024",
        productionHouse = "Blue Hour Films",
    )

    private fun sentPatch(base: CreditsForm, next: CreditsForm): JsonObject {
        server.enqueue(MockResponse.Builder().code(200).body("""{"data":{"id":"p1"}}""").build())
        runBlocking { edit.update("p1", creditsPatch(base, next)) }
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.target).isEqualTo("/v1/posts/p1")
        return app.parseToJsonElement(request.body!!.utf8()).jsonObject
    }

    @Test
    fun `setting the credits sends exactly patch_request`() {
        val sent = sentPatch(CreditsForm(), patchForm)

        assertThat(sent).isEqualTo(envelopeless("patch_request.json").asServerReadsPatch())
    }

    @Test
    fun `clearing everything sends exactly patch_clear_request - empty lists and explicit nulls`() {
        val sent = sentPatch(patchForm, CreditsForm())

        assertThat(sent).isEqualTo(envelopeless("patch_clear_request.json").asServerReadsPatch())
        assertThat(sent.getValue("release_year")).isEqualTo(JsonNull)
        assertThat(sent.getValue("production_house")).isEqualTo(JsonNull)
    }

    /** The drafts and the create carry the same credit items the form builds. */
    @Test
    fun `the create and both draft fixtures carry the credit items the form builds`() {
        val createForm = patchForm.copy(
            entries = listOf(
                CreditEntry("a", "Asha Rao", "Director", CreditPerson(ASHA, "Asha Rao")),
                CreditEntry("r", "Ravi Kumar", "Actor", CreditPerson(RAVI, "Ravi Kumar")),
                CreditEntry("b", "Meera Iyer", "Music"),
            ),
        )
        val draftForm = patchForm.copy(genres = listOf("drama"))
        val bodies = mapOf(
            envelopeless("create_request.json") to createForm,
            envelopeless("reel_draft_patch_request.json") to draftForm,
            envelopeless("post_draft_request.json").getValue("payload").jsonObject to draftForm,
        )

        bodies.forEach { (body, form) ->
            strict.decodeFromJsonElement(ListSerializer(CreditInputDto.serializer()), body.getValue("credits"))
            assertThat(app.encodeToJsonElement(ListSerializer(CreditInputDto.serializer()), form.toInput()))
                .isEqualTo(body.getValue("credits"))
            assertThat(body.getValue("genres").jsonArray.map { it.jsonPrimitive.content }).isEqualTo(form.genres)
            assertThat(body.getValue("release_year").jsonPrimitive.content).isEqualTo(form.releaseYear)
            assertThat(body.getValue("production_house").jsonPrimitive.content).isEqualTo(form.productionHouse)
        }
    }

    /** A request fixture is the body itself, with no `{data}` envelope. */
    private fun envelopeless(name: String): JsonObject = app.parseToJsonElement(read(name)).jsonObject

    /**
     * The PATCH fixture as post-service reads it (`updatePostRequest`): every
     * field but the two credits facts is a Go pointer, where `null` and
     * absent are the same "leave it" — so a `null` there is dropped. On
     * `release_year` and `production_house` (raw JSON) `null` is the CLEAR,
     * and absent is "leave it": those keep their `null`.
     */
    private fun JsonObject.asServerReadsPatch(): JsonObject =
        JsonObject(filter { (key, value) -> value !is JsonNull || key in NULL_IS_CLEAR })

    private companion object {
        const val OWNER = "d2222222-2222-4222-8222-222222222222"
        const val ASHA = "d3333333-3333-4333-8333-333333333333"
        const val RAVI = "d4444444-4444-4444-8444-444444444444"
        const val DEV = "d5555555-5555-4555-8555-555555555555"
        const val RAVI_CREDIT = "e2222222-2222-4222-8222-222222222222"

        val NULL_IS_CLEAR = setOf("release_year", "production_house")

        val CLAIMED = setOf(
            "accept_response.json",
            "appearances.json",
            "create_request.json",
            "create_response.json",
            "decline_response.json",
            "error_credit_not_found.json",
            "error_credits_not_allowed.json",
            "error_invalid_genre.json",
            "genres.json",
            "patch_clear_request.json",
            "patch_request.json",
            "post_detail_owner.json",
            "post_detail_viewer.json",
            "post_draft_request.json",
            "reel_draft_patch_request.json",
            // reel_draft_response.json is not here: its only reader is the Creator Hub's drafts
            // normaliser, so it is copied into feature/tube and read there.
            "remove_response.json",
            "requests.json",
        )
    }
}
