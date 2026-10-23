package com.us.android.core.feed.ui.copyright

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.copyright.CopyrightReviewApi
import com.us.android.core.feed.data.copyright.CopyrightReviewRepository
import com.us.android.core.feed.data.copyright.CopyrightReviewRequestBody
import com.us.android.core.feed.data.copyright.CopyrightReviews
import com.us.android.core.model.CopyrightReviewState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonElement
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * The "Request review" sheet (copyright title review, 2026-10-24): a note
 * of 1–1000 characters, sent once to
 * `POST /v1/posts/:id/copyright-review/request` (post-service
 * http/copyright_review.go:22-25); the answer is recorded for every surface,
 * and each refusal (:322-334) is said in words.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CopyrightRequestViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val json = NetworkModule.provideJson()

    private class FakeApi(private val json: kotlinx.serialization.json.Json) : CopyrightReviewApi {
        val sent = mutableListOf<Pair<String, CopyrightReviewRequestBody>>()
        var failWith: Pair<Int, String>? = null

        override suspend fun request(postId: String, body: CopyrightReviewRequestBody): ApiEnvelope<JsonElement> {
            sent += postId to body
            failWith?.let { (code, error) ->
                throw HttpException(Response.error<Any>(code, error.toResponseBody("application/json".toMediaType())))
            }
            return ApiEnvelope(
                data = json.parseToJsonElement(
                    """{"state":"pending","matched_title":"Leo","created_at":"2026-10-24T08:00:01Z","review_requested_at":"2026-10-24T09:30:00Z"}""",
                ),
            )
        }

        override suspend fun detail(postId: String): ApiEnvelope<JsonElement> = error("not read here")
    }

    private val api = FakeApi(json)
    private val store = CopyrightReviews()
    private val viewModel = CopyrightRequestViewModel(CopyrightReviewRepository(api, ErrorMapper(json), store), store)

    @Test
    fun `a note is sent once, trimmed, and the answer reaches every surface`() = runTest(dispatcher) {
        viewModel.changeNote("  I own the rights  ")
        assertThat(viewModel.state.value.canSend).isTrue()
        viewModel.send("p1")
        viewModel.send("p1") // a second tap while sending does nothing
        assertThat(viewModel.state.value.sending).isTrue()
        advanceUntilIdle()

        assertThat(api.sent).containsExactly("p1" to CopyrightReviewRequestBody("I own the rights"))
        val sent = viewModel.state.value.sent!!
        assertThat(sent.state).isEqualTo(CopyrightReviewState.PENDING)
        assertThat(sent.reviewRequestedAt).isEqualTo("2026-10-24T09:30:00Z")
        assertThat(viewModel.reviews.value["p1"]).isEqualTo(sent)
        assertThat(viewModel.state.value.sending).isFalse()
    }

    @Test
    fun `an empty or overlong note is refused here, with the reason, and nothing is sent`() = runTest(dispatcher) {
        viewModel.send("p1")
        assertThat(viewModel.state.value.problem).isEqualTo("Tell us why you have the rights to this video.")
        viewModel.changeNote("a".repeat(1001))
        assertThat(viewModel.state.value.problem).isNull() // typing clears the last problem
        assertThat(viewModel.state.value.canSend).isFalse()
        assertThat(viewModel.state.value.remaining).isEqualTo(-1)
        viewModel.send("p1")
        assertThat(viewModel.state.value.problem).isEqualTo("Keep it to 1000 characters.")
        advanceUntilIdle()
        assertThat(api.sent).isEmpty()
    }

    @Test
    fun `a decided review says so, and the sheet stays open to read it`() = runTest(dispatcher) {
        api.failWith = 409 to """{"error":{"code":"REVIEW_ALREADY_DECIDED","message":"decided"}}"""
        viewModel.changeNote("mine")
        viewModel.send("p1")
        advanceUntilIdle()
        assertThat(viewModel.state.value.problem).isEqualTo("This review has already been decided. Refresh to see the result.")
        assertThat(viewModel.state.value.sent).isNull()
        assertThat(store.reviews.value).isEmpty()
    }

    @Test
    fun `opening the sheet again starts a fresh note`() = runTest(dispatcher) {
        viewModel.changeNote("half a thought")
        viewModel.open()
        assertThat(viewModel.state.value.note).isEmpty()
        assertThat(viewModel.state.value.remaining).isEqualTo(1000)
    }
}
