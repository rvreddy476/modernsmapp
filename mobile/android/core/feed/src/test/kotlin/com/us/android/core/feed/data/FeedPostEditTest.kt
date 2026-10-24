package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
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
 * A feed post's Edit post and Pin to profile (2026-10-24), against post-service:
 * `PATCH /v1/posts/:id` (internal/http/post_edit.go:54-100, every field a
 * pointer, absent = leave it) and `PUT /v1/posts/:id/pin {pinned}`
 * (internal/http/handler.go:1443-1475: `{"data":{"pinned":…}}`, or 409
 * `PIN_LIMIT` with `details.max = 3`).
 */
class FeedPostEditTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private val base = FeedPostEditForm(text = "hello", visibility = "public", allowComments = true, hideLikeCount = false)

    // ── The PATCH ───────────────────────────────────────────────────────

    @Test
    fun `nothing moved is an empty patch`() {
        assertThat(feedPostEditPatch(base, base).isEmpty).isTrue()
    }

    @Test
    fun `only the moved fields are sent, and never the members-only gate or the action`() {
        val patch = feedPostEditPatch(base, base.copy(text = "hello world", allowComments = false))

        assertThat(patch).isEqualTo(PostEditPatch(text = "hello world", noComments = true))
        val wire = json.encodeToString(PostEditPatch.serializer(), patch)
        assertThat(wire).isEqualTo("""{"text":"hello world","no_comments":true}""")
        assertThat(wire).doesNotContain("tier_required_id")
        assertThat(wire).doesNotContain("action")
    }

    @Test
    fun `visibility and the like count move on their own`() {
        assertThat(feedPostEditPatch(base, base.copy(visibility = "followers")))
            .isEqualTo(PostEditPatch(visibility = "followers"))
        assertThat(feedPostEditPatch(base, base.copy(hideLikeCount = true)))
            .isEqualTo(PostEditPatch(hideLikeCount = true))
        assertThat(feedPostEditPatch(base.copy(allowComments = false), base))
            .isEqualTo(PostEditPatch(noComments = false))
    }

    @Test
    fun `a visibility the route does not take is never sent`() {
        assertThat(feedPostEditPatch(base, base.copy(visibility = "scheduled")).isEmpty).isTrue()
        assertThat(feedPostEditPatch(base, base.copy(visibility = "unlisted"))).isEqualTo(PostEditPatch(visibility = "unlisted"))
    }

    @Test
    fun `the form reads the owner detail`() {
        val form = PostEditDetailDto(text = "t", visibility = "", noComments = true, hideLikeCount = true).toFeedPostEditForm()

        assertThat(form).isEqualTo(FeedPostEditForm(text = "t", visibility = "public", allowComments = false, hideLikeCount = true))
    }

    @Test
    fun `the visibility choices are the composer's four, plus the post's own`() {
        assertThat(FeedPostVisibility.choicesFor("public").map { it.wire })
            .containsExactly("public", "followers", "friends", "private").inOrder()
        assertThat(FeedPostVisibility.choicesFor("unlisted").last()).isEqualTo(EditChoice("unlisted", "Unlisted"))
        assertThat(FeedPostVisibility.choicesFor("circle").last()).isEqualTo(EditChoice("circle", "Current setting"))
    }

    // ── The checks before Save ──────────────────────────────────────────

    @Test
    fun `text past 5000 code points is stopped before it is sent`() {
        val long = "a".repeat(FEED_POST_TEXT_MAX + 1)

        assertThat(feedPostEditProblem(base, base.copy(text = long), hasMedia = false))
            .isEqualTo("Posts can be up to 5,000 characters. This one is 5,001.")
        assertThat(feedPostEditProblem(base, base.copy(text = "a".repeat(FEED_POST_TEXT_MAX)), hasMedia = false)).isNull()
    }

    @Test
    fun `clearing the text of a post with no media is stopped, with media it is allowed`() {
        assertThat(feedPostEditProblem(base, base.copy(text = "  "), hasMedia = false))
            .isEqualTo("A post without a photo or video needs some text.")
        assertThat(feedPostEditProblem(base, base.copy(text = ""), hasMedia = true)).isNull()
    }

    @Test
    fun `the server's codes read as the web's sentences`() {
        assertThat(feedPostEditRefusal(AppError.Unknown(code = "TEXT_TOO_LONG", statusCode = 422)))
            .isEqualTo("Posts can be up to 5,000 characters. Shorten the text and try again.")
        assertThat(feedPostEditRefusal(AppError.Forbidden(code = "FORBIDDEN")))
            .isEqualTo("Only the person who posted this can edit it.")
        assertThat(feedPostEditRefusal(AppError.NotFound())).isEqualTo("This post no longer exists.")
        assertThat(feedPostEditRefusal(AppError.Unknown(code = "SOMETHING_NEW", statusCode = 422)))
            .isEqualTo("Your changes couldn't be saved. Please try again.")
    }

    // ── Pin ─────────────────────────────────────────────────────────────

    @Test
    fun `only a 409 PIN_LIMIT on a pin says how many`() {
        assertThat(pinRefusal(AppError.Unknown(code = "PIN_LIMIT", statusCode = 409), pinning = true))
            .isEqualTo("You can pin up to 3 posts. Unpin one first.")
        assertThat(pinRefusal(AppError.Unknown(code = "CONFLICT", statusCode = 409), pinning = true))
            .isEqualTo("That post couldn't be pinned. Please try again.")
        assertThat(pinRefusal(AppError.Unknown(code = "PIN_LIMIT", statusCode = 500), pinning = true))
            .isEqualTo("That post couldn't be pinned. Please try again.")
        assertThat(pinRefusal(AppError.Unknown(code = "PIN_LIMIT", statusCode = 409), pinning = false))
            .isEqualTo("That post couldn't be unpinned. Please try again.")
        assertThat(pinRefusal(AppError.NoNetwork(), pinning = true))
            .isEqualTo("You seem to be offline. Check your connection and try again.")
        assertThat(MAX_PINNED_POSTS).isEqualTo(3)
    }

    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private var answer: Pair<Int, String> = 200 to """{"data":{"pinned":true}}"""
    private lateinit var pins: PostPins
    private lateinit var repository: PostPinRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                return MockResponse.Builder().code(answer.first)
                    .setHeader("Content-Type", "application/json").body(answer.second).build()
            }
        }
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        pins = PostPins()
        repository = PostPinRepository(retrofit.create(PostPinApi::class.java), pins, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `pin is a PUT with the pinned flag and the session learns it`() = runBlocking {
        val result = repository.setPinned("p1", pinned = true)

        assertThat(result).isEqualTo(AppResult.Success(true))
        val sent = requests.single()
        assertThat("${sent.method} ${sent.url.encodedPath}").isEqualTo("PUT /v1/posts/p1/pin")
        assertThat(sent.body!!.utf8()).isEqualTo("""{"pinned":true}""")
        assertThat(pins.pins.value).containsExactly("p1", true)
        Unit
    }

    @Test
    fun `a 409 PIN_LIMIT arrives with its code and status, and nothing is recorded`() = runBlocking {
        answer = 409 to """{"error":{"code":"PIN_LIMIT","message":"You can pin up to 3 posts. Unpin one to pin this.","details":{"max":3}}}"""

        val result = repository.setPinned("p4", pinned = true)

        val error = (result as AppResult.Failure).error
        assertThat(pinRefusal(error, pinning = true)).isEqualTo(PIN_LIMIT_MESSAGE)
        assertThat(pins.pins.value).isEmpty()
    }
}
