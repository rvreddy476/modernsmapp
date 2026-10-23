package com.us.android.core.feed.data.copyright

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.PostEditDetailDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.toDomain
import com.us.android.core.feed.ui.edit.PostEditUiState
import com.us.android.core.memberships.data.membersOnlyRefusal
import com.us.android.core.model.CopyrightCopy
import com.us.android.core.model.CopyrightReview
import com.us.android.core.model.CopyrightReviewState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.File

/**
 * The copyright title review's wire on Android (server commit dbdd1c0ea).
 *
 * The fixtures under `contracts/copyright/` are written from the server's
 * own shapes, as no golden capture exists yet:
 *  - `copyright_review` — post-service service/copyright_title_review.go:651-658
 *    `{state, matched_title, created_at, decided_at?, review_requested_at?}`,
 *    on the owner's detail (service/post.go:615) and uploads rows (my_uploads.go:399);
 *  - uploads rows' `flags` (my_uploads.go:36-45, `copyright_hold` at :70) and
 *    `restrictions` (my_uploads.go:77-83; source `title_review`,
 *    store/postgres/copyright_title_review.go:28, reason `title_match` at :33);
 *  - the request route and its refusals, http/copyright_review.go:22-25 and :316-334;
 *  - 403 UPLOADS_PAUSED with `details.until` (http/standing_errors.go:53-55) and
 *    409 COPYRIGHT_REVIEW (standing_errors.go:75-78).
 */
class CopyrightReviewContractTest {

    private val json = NetworkModule.provideJson()
    private val mapper = ErrorMapper(json)
    private lateinit var server: MockWebServer
    private lateinit var store: CopyrightReviews
    private lateinit var repository: CopyrightReviewRepository

    private fun fixture(name: String) = File("src/test/resources/contracts/copyright/$name").readText()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CopyrightReviewApi::class.java)
        store = CopyrightReviews()
        repository = CopyrightReviewRepository(api, mapper, store)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun httpError(code: Int, fixtureName: String): AppError = mapper.map(
        HttpException(Response.error<Any>(code, fixture(fixtureName).toResponseBody("application/json".toMediaType()))),
    )

    // ── decoding ─────────────────────────────────────────────────────────

    @Test
    fun `the owner's detail carries the pending review, and a feed row reads it into the item`() {
        val detail = json.decodeFromString(ApiEnvelope.serializer(FeedItemDto.serializer()), fixture("post_detail_held.json"))
        val item = detail.data!!.toDomain()
        assertThat(item.copyrightReview).isEqualTo(
            CopyrightReview(
                state = CopyrightReviewState.PENDING,
                matchedTitle = "Kalki 2898 AD",
                createdAt = "2026-10-24T08:00:01Z",
            ),
        )
    }

    @Test
    fun `the Edit sheet's detail carries it too, and a row without one has none`() {
        val raw = json.parseToJsonElement(fixture("post_detail_held.json")).jsonObject["data"].toString()
        val edit = json.decodeFromString(PostEditDetailDto.serializer(), raw)
        assertThat(edit.copyrightReview.toCopyrightReview()?.state).isEqualTo(CopyrightReviewState.PENDING)
        val open = json.decodeFromString(PostEditDetailDto.serializer(), """{"id":"p1"}""")
        assertThat(open.copyrightReview.toCopyrightReview()).isNull()
        assertThat(json.decodeFromString(FeedItemDto.serializer(), """{"id":"p1"}""").toDomain().copyrightReview).isNull()
    }

    @Test
    fun `uploads rows read each state, the hold flag and the title-review restriction`() {
        val rows = json.parseToJsonElement(fixture("uploads_videos.json")).jsonObject["data"] as JsonArray
        val o = rows.map { it as JsonObject }
        val pending = o[0]["copyright_review"].toCopyrightReview()!!
        assertThat(pending.state).isEqualTo(CopyrightReviewState.PENDING)
        assertThat(pending.reviewRequestedAt).isEqualTo("2026-10-24T09:30:00Z")
        assertThat(o[1]["copyright_review"].toCopyrightReview()!!.decidedAt).isEqualTo("2026-10-21T10:00:00Z")
        assertThat(o[2]["copyright_review"].toCopyrightReview()!!.state).isEqualTo(CopyrightReviewState.CLEARED)
        assertThat(o[3]["copyright_review"].toCopyrightReview()).isNull()

        assertThat(o.map { it["flags"].hasCopyrightHoldFlag() }).containsExactly(true, true, false, true, false).inOrder()
        assertThat(o.map { it["restrictions"].hasTitleReviewRestriction() })
            .containsExactly(true, true, false, false, false).inOrder()
    }

    @Test
    fun `a stray shape is no review rather than a crash`() {
        fun read(raw: String): CopyrightReview? = json.parseToJsonElement(raw).toCopyrightReview()
        assertThat(read("""null""")).isNull()
        assertThat(read("""[]""")).isNull()
        assertThat(read("""{"state":"under_review","matched_title":"X"}""")).isNull()
        assertThat(read("""{"state":7}""")).isNull()
        val lenient = read("""{"state":"blocked","matched_title":42,"created_at":"t","decided_at":""}""")!!
        assertThat(lenient.matchedTitle).isEmpty()
        assertThat(lenient.decidedAt).isNull()
        assertThat((null as JsonElement?).toCopyrightReview()).isNull()
        assertThat(json.parseToJsonElement("""["copyright_hold"]""").hasCopyrightHoldFlag()).isTrue()
        assertThat(json.parseToJsonElement("""[1,{"x":1},"review_hold"]""").hasCopyrightHoldFlag()).isFalse()
        assertThat(json.parseToJsonElement("""{"source":"title_review"}""").hasTitleReviewRestriction()).isFalse()
    }

    // ── refusals ─────────────────────────────────────────────────────────

    @Test
    fun `403 UPLOADS_PAUSED keeps its date through the mapper and reads with it`() {
        val error = httpError(403, "error_uploads_paused.json")
        assertThat(error).isInstanceOf(AppError.Forbidden::class.java)
        val message = error.uploadsPausedMessage()!!
        assertThat(message).startsWith("Long-video uploads are paused until ")
        assertThat(message).contains("2026")
        assertThat(message).endsWith("after repeated copyright blocks. Reels and posts still work.")
        // Any other 403 is not the pause.
        assertThat(AppError.Forbidden(code = "CHANNEL_REQUIRED").uploadsPausedMessage()).isNull()
        assertThat(AppError.Unknown(code = "UPLOADS_PAUSED", statusCode = 409).uploadsPausedMessage()).isNull()
    }

    @Test
    fun `409 COPYRIGHT_REVIEW is the members-only refusal in the contract's words`() {
        val error = httpError(409, "error_copyright_review_tier.json")
        assertThat(error.isCopyrightReviewTier()).isTrue()
        assertThat(membersOnlyRefusal("COPYRIGHT_REVIEW")).isEqualTo(CopyrightCopy.MEMBERS_ONLY_HELD)
        assertThat(membersOnlyRefusal("INVALID_TIER")).isNotEqualTo(CopyrightCopy.MEMBERS_ONLY_HELD)
        assertThat(AppError.Unknown(code = "INVALID_TIER", statusCode = 400).isCopyrightReviewTier()).isFalse()
    }

    @Test
    fun `each refusal of Request review has its own sentence`() {
        val gone = "This video has no copyright review to answer any more."
        assertThat(copyrightRequestRefusal(httpError(404, "error_review_not_found.json"))).isEqualTo(gone)
        assertThat(copyrightRequestRefusal(AppError.Unknown(code = "REVIEW_NOT_FOUND", statusCode = 404))).isEqualTo(gone)
        assertThat(copyrightRequestRefusal(httpError(409, "error_review_decided.json")))
            .isEqualTo("This review has already been decided. Refresh to see the result.")
        val unavailable = "Copyright review is unavailable right now. Try again in a little while."
        assertThat(copyrightRequestRefusal(httpError(503, "error_review_unavailable.json"))).isEqualTo(unavailable)
        assertThat(copyrightRequestRefusal(AppError.Server(statusCode = 503, code = null))).isEqualTo(unavailable)
        assertThat(copyrightRequestRefusal(httpError(400, "error_note_invalid.json")))
            .isEqualTo("Write a note of 1 to 1000 characters.")
        assertThat(copyrightRequestRefusal(AppError.NoNetwork())).isEqualTo("You're offline. Try again when you're connected.")
        assertThat(copyrightRequestRefusal(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR")))
            .isEqualTo("Your request didn't go through. Try again.")
    }

    // ── the request ──────────────────────────────────────────────────────

    @Test
    fun `Request review posts the trimmed note to the post's route and records the answer`() {
        enqueue(fixture("request_review_ok.json"))
        val result = runBlocking { repository.request("p1", "  I own the rights\n") }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("POST")
        assertThat(sent.target).isEqualTo("/v1/posts/p1/copyright-review/request")
        assertThat(sent.body!!.utf8()).isEqualTo("""{"note":"I own the rights"}""")
        val review = (result as AppResult.Success).data
        assertThat(review.reviewRequestedAt).isEqualTo("2026-10-24T09:30:00Z")
        assertThat(store.reviews.value["p1"]).isEqualTo(review)
    }

    @Test
    fun `a note the server would refuse never leaves the phone`() {
        val empty = runBlocking { repository.request("p1", "   ") }
        val long = runBlocking { repository.request("p1", "a".repeat(1001)) }
        assertThat((empty as AppResult.Failure).error).isInstanceOf(AppError.InvalidRequest::class.java)
        assertThat((long as AppResult.Failure).error).isInstanceOf(AppError.InvalidRequest::class.java)
        assertThat(server.requestCount).isEqualTo(0)
        assertThat(store.reviews.value).isEmpty()
    }

    @Test
    fun `a refused request records nothing and keeps the server's code`() {
        enqueue(fixture("error_review_decided.json"), code = 409)
        val result = runBlocking { repository.request("p1", "mine") }
        val error = (result as AppResult.Failure).error
        assertThat((error as AppError.Unknown).code).isEqualTo("REVIEW_ALREADY_DECIDED")
        assertThat(store.reviews.value).isEmpty()
    }

    @Test
    fun `the publish re-read finds the review on the detail, and a failed read is no review`() {
        enqueue(fixture("post_detail_held.json"))
        val held = runBlocking { repository.reviewOf("p9") }
        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/p9")
        assertThat(held?.state).isEqualTo(CopyrightReviewState.PENDING)
        assertThat(store.reviews.value["p9"]).isEqualTo(held)

        enqueue("""{"data":{"id":"p8"}}""")
        assertThat(runBlocking { repository.reviewOf("p8") }).isNull()
        enqueue("""{"error":{"code":"NOT_FOUND"}}""", code = 404)
        assertThat(runBlocking { repository.reviewOf("p7") }).isNull()
    }

    @Test
    fun `the request body is only the note`() {
        assertThat(json.encodeToString(CopyrightReviewRequestBody.serializer(), copyrightRequestBody(" x ")))
            .isEqualTo("""{"note":"x"}""")
    }

    @Test
    fun `the Edit sheet knows a held video cannot be members-only`() {
        val pending = CopyrightReview(CopyrightReviewState.PENDING, "T", "t")
        assertThat(PostEditUiState(copyrightReview = pending).copyrightHeld).isTrue()
        assertThat(PostEditUiState(copyrightReview = pending.copy(state = CopyrightReviewState.BLOCKED)).copyrightHeld).isTrue()
        assertThat(PostEditUiState(copyrightReview = pending.copy(state = CopyrightReviewState.CLEARED)).copyrightHeld).isFalse()
        assertThat(PostEditUiState().copyrightHeld).isFalse()
    }

    @Test
    fun `a review learned this session wins over the one a list read`() {
        val listed = CopyrightReview(CopyrightReviewState.PENDING, "T", "t")
        val learned = listed.copy(reviewRequestedAt = "2026-10-24T09:30:00Z")
        assertThat(mapOf("p1" to learned).over("p1", listed)).isEqualTo(learned)
        assertThat(mapOf("p2" to learned).over("p1", listed)).isEqualTo(listed)
        assertThat(emptyMap<String, CopyrightReview>().over("p1", null)).isNull()
    }
}
