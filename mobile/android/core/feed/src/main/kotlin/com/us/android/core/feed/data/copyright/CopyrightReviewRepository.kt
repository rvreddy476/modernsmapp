package com.us.android.core.feed.data.copyright

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.CopyrightCopy
import com.us.android.core.model.CopyrightReview
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The author's two copyright-review calls (post-service http/copyright_review.go:21-29):
 *
 *     POST /v1/posts/:id/copyright-review/request  {"note": 1-1000 chars}
 *          → 200 the copyright_review view; 404 REVIEW_NOT_FOUND; 409 REVIEW_ALREADY_DECIDED;
 *            503 COPYRIGHT_REVIEW_UNAVAILABLE
 *     GET  /v1/posts/:id                           the post detail, read for its copyright_review
 *          (after a long video is published: the create's answer does not carry it)
 */
interface CopyrightReviewApi {

    @POST("v1/posts/{postId}/copyright-review/request")
    suspend fun request(
        @Path("postId") postId: String,
        @Body body: CopyrightReviewRequestBody,
    ): ApiEnvelope<JsonElement>

    /** Raw: only `copyright_review` is read from it. */
    @GET("v1/posts/{postId}")
    suspend fun detail(@Path("postId") postId: String): ApiEnvelope<JsonElement>
}

/** The request body: the note, trimmed (http/copyright_review.go:299-301). */
@Serializable
data class CopyrightReviewRequestBody(val note: String)

/** The body "Request review" sends for [note]: trimmed, as the server stores it. */
fun copyrightRequestBody(note: String): CopyrightReviewRequestBody = CopyrightReviewRequestBody(note.trim())

/**
 * The reviews this session has learned since the lists were read, by post
 * id: a sent "Request review" is what the hub row, the Edit sheet and the
 * watch screen show at once, without a refetch. Process-wide and in memory,
 * like `PostEdits`.
 */
@Singleton
class CopyrightReviews @Inject constructor() {

    private val _reviews = MutableStateFlow<Map<String, CopyrightReview>>(emptyMap())
    val reviews: StateFlow<Map<String, CopyrightReview>> = _reviews.asStateFlow()

    fun record(postId: String, review: CopyrightReview) = _reviews.update { it + (postId to review) }
}

/** [review] as this session last learned it for [postId]. */
fun Map<String, CopyrightReview>.over(postId: String, review: CopyrightReview?): CopyrightReview? = this[postId] ?: review

@Singleton
class CopyrightReviewRepository @Inject constructor(
    private val api: CopyrightReviewApi,
    private val errorMapper: ErrorMapper,
    private val reviews: CopyrightReviews,
) {

    /**
     * Sends the author's note. A note the server would refuse is refused
     * here first, without a round trip ([CopyrightCopy.noteProblem]); a
     * landed request is recorded in [CopyrightReviews].
     */
    suspend fun request(postId: String, note: String): AppResult<CopyrightReview> {
        CopyrightCopy.noteProblem(note)?.let { problem ->
            return AppResult.Failure(AppError.InvalidRequest(message = problem))
        }
        return when (val result = apiCall(errorMapper) { api.request(postId, copyrightRequestBody(note)) }) {
            is AppResult.Success -> {
                val review = result.data.toCopyrightReview()
                if (review == null) {
                    AppResult.Failure(AppError.Malformed(detail = "the answer carried no copyright review"))
                } else {
                    reviews.record(postId, review)
                    AppResult.Success(review)
                }
            }
            is AppResult.Failure -> result
        }
    }

    /**
     * The post's review as its author reads it, or null when it has none or
     * the read failed — a publish tile only adds the notice when it is sure.
     */
    suspend fun reviewOf(postId: String): CopyrightReview? {
        val result = apiCall(errorMapper) { api.detail(postId) }
        val detail = (result as? AppResult.Success)?.data as? JsonObject ?: return null
        return detail["copyright_review"].toCopyrightReview()?.also { reviews.record(postId, it) }
    }
}
