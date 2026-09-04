package com.us.android.feature.post.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.PostDto
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * Post and interaction endpoints.
 *
 * A Retrofit interface, not a client: created from the app-wide `Retrofit`, so
 * it inherits the base URL, auth, single-flight refresh, tracing and error
 * mapping. Every path here was exercised live through the gateway; see
 * prompt/android-api-contracts.md §2, and treat its 2026-08-17 sections as
 * canonical where they supersede the original capture.
 *
 * Retry is opt-in for non-GET methods. Only the bookmark pair carries
 * `@Retryable`, because only that pair has been proven idempotent by repeated
 * live calls. Reactions and reposts do not honour an idempotency key, so a
 * replayed one is a second write rather than a no-op.
 */
interface PostApi {

    /** Public read. 404 `NOT_FOUND` when absent. */
    @GET("v1/posts/{postId}")
    suspend fun getPost(@Path("postId") postId: String): ApiEnvelope<PostDto>

    /**
     * Creates a post. THE ONLY create-post declaration in the app.
     *
     * `Idempotency-Key` is a REQUIRED, non-null UUID parameter, not a
     * convenience: the server refuses a create without one (400
     * `MISSING_IDEMPOTENCY_KEY`) because "committed, response lost" is the
     * normal outcome of publishing from a phone, and a retry without a stable
     * key is a duplicate post. The caller mints the key when it freezes the
     * publish snapshot and reuses it for every retry of those exact bytes.
     *
     * NO `@Retryable`. The transport-level retry is opt-in for non-GET methods
     * and stays off here until the durable-idempotency proof (C-LB-3, criteria
     * 1-6) passes live — an automatic retry over a create that is not yet
     * proven exactly-once is a duplicate-post generator.
     */
    @POST("v1/posts")
    suspend fun createPost(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreatePostRequest,
    ): ApiEnvelope<PostDto>
}

/**
 * The reel category list — `{"data":[{"id":"comedy","label":"Comedy"},…]}`.
 *
 * Its own interface rather than a method on [PostApi]: the read is
 * reel-only, and every fake `PostApi` in the tests would otherwise grow a
 * member it never calls. Landed server-side 2026-09-04; the reel form still
 * keeps a fallback list for when the call fails.
 */
interface PostCategoriesApi {

    @GET("v1/posts/categories")
    suspend fun categories(): ApiEnvelope<List<PostCategoryDto>>
}

@Serializable
data class PostCategoryDto(
    val id: String = "",
    val label: String = "",
)
