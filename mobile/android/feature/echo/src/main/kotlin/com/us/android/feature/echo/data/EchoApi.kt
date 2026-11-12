package com.us.android.feature.echo.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Echo's routes (docs/designs/echo-contract.md "Routes"): `/v1/notes*` on
 * post-service and `/v1/feed/notes/...` on feed-service, all in the standard
 * envelope. Likes and plain reposts are the existing post routes, through
 * :core:engagement, not here.
 */
@Suppress("TooManyFunctions") // one function per contract route
interface EchoApi {

    @GET("v1/feed/notes/following")
    suspend fun following(
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    @GET("v1/feed/notes/for-you")
    suspend fun forYou(
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    @GET("v1/notes/{id}")
    suspend fun note(@Path("id") id: String): ApiEnvelope<NoteDto>

    @GET("v1/notes/{id}/replies")
    suspend fun replies(
        @Path("id") id: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    /** From the top note down to the parent of [id]; no paging. */
    @GET("v1/notes/{id}/conversation")
    suspend fun conversation(@Path("id") id: String): ApiEnvelope<NoteListDto>

    @GET("v1/notes/users/{userId}")
    suspend fun byUser(
        @Path("userId") userId: String,
        @Query("tab") tab: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    @GET("v1/notes/me/eligibility")
    suspend fun eligibility(): ApiEnvelope<EligibilityDto>

    @POST("v1/notes")
    suspend fun create(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreateNoteRequest,
    ): ApiEnvelope<NoteDto>

    @POST("v1/notes/thread")
    suspend fun createThread(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreateThreadRequest,
    ): ApiEnvelope<NoteThreadDto>

    @PATCH("v1/notes/{id}")
    suspend fun edit(@Path("id") id: String, @Body body: EditNoteRequest): ApiEnvelope<NoteDto>

    /** `204` with no body: declared as Unit so Retrofit skips conversion. */
    @DELETE("v1/notes/{id}")
    suspend fun delete(@Path("id") id: String)
}
