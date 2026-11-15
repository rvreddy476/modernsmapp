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
 * Echo's routes (docs/designs/echo-contract.md "Routes" and
 * echo-v2-contract.md section 2): `/v1/notes*` on post-service and
 * `/v1/feed/notes/...` on feed-service, all in the standard envelope. Likes,
 * plain reposts and the share count are the existing post routes, through
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

    @GET("v1/notes/{id}/thread")
    suspend fun thread(@Path("id") id: String): ApiEnvelope<NoteListDto>

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

    // ── Echo v2 ───────────────────────────────────────────────────────

    /** One vote per person, final. `409 POLL_CLOSED` / `ALREADY_VOTED`, `403 OWN_POLL`. */
    @POST("v1/notes/{id}/poll/vote")
    suspend fun vote(@Path("id") id: String, @Body body: VoteRequest): ApiEnvelope<NoteDto>

    /** The author's scheduled notes, soonest first. */
    @GET("v1/notes/me/scheduled")
    suspend fun scheduled(
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    @PATCH("v1/notes/{id}/schedule")
    suspend fun reschedule(@Path("id") id: String, @Body body: ScheduleRequest): ApiEnvelope<NoteDto>

    /** `204`: deletes the scheduled note. */
    @DELETE("v1/notes/{id}/schedule")
    suspend fun deleteScheduled(@Path("id") id: String)

    @POST("v1/notes/{id}/bookmark")
    suspend fun bookmark(@Path("id") id: String): ApiEnvelope<BookmarkDto>

    @DELETE("v1/notes/{id}/bookmark")
    suspend fun unbookmark(@Path("id") id: String): ApiEnvelope<BookmarkDto>

    /** Newest bookmark first. */
    @GET("v1/notes/bookmarks")
    suspend fun bookmarks(
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    /** `204`. Up to 50 ids; a signed-in viewer counts once per note per 24 hours. */
    @POST("v1/notes/views")
    suspend fun views(@Body body: ViewsRequest)

    @GET("v1/notes/{id}/quotes")
    suspend fun quotes(
        @Path("id") id: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    @GET("v1/notes/trends")
    suspend fun trends(@Query("limit") limit: Int?): ApiEnvelope<TrendListDto>

    @GET("v1/notes/hashtags/{tag}")
    suspend fun hashtag(
        @Path("tag") tag: String,
        @Query("tab") tab: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>

    @GET("v1/notes/search")
    suspend fun search(
        @Query("q") query: String,
        @Query("tab") tab: String,
        @Query("cursor") cursor: String?,
        @Query("limit") limit: Int?,
    ): ApiEnvelope<NoteListDto>
}
