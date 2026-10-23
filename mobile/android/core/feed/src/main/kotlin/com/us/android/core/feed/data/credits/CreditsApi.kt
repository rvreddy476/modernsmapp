package com.us.android.core.feed.data.credits

import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.network.ApiEnvelope
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Video credits (contract pinned 2026-10-23, as the server landed). Credits
 * themselves ride the post: set on `POST /v1/posts` and replaced with
 * `PATCH /v1/posts/:id` (see `CreatePostRequest` and `PostEditPatch`), and
 * read back ONLY on `GET /v1/posts/:id` and those two writes' answers — no
 * feed, list or appearances row carries them. These are the rest:
 *
 *     GET    /v1/posts/genres                      [{slug,label}]
 *     GET    /v1/credits/requests?cursor=          my pending requests, meta.next_cursor
 *     POST   /v1/credits/:id/accept                {status:"accepted"}  the tagged user, from any linked status
 *     POST   /v1/credits/:id/decline               {status:"declined"}  the credit stays as plain text
 *     DELETE /v1/credits/:id                       {status:"unlinked"}  the tagged user, pending or accepted:
 *                                                  "Remove me from this video"; a repeat is 404
 *     GET    /v1/creators/:creatorId/appearances   ?cursor=&limit=  videos that person ACCEPTED a credit
 *                                                  on, newest first, meta.next_cursor
 *
 * "Appears in" sits under `/v1/creators`, not `/v1/users`: the gateway sends
 * `/v1/users/...` to user-service, which has no such route. `creatorId` is
 * the person's USER id.
 *
 * Its own interface rather than more members on `VideoFeedApi`: the fakes of
 * that one would otherwise grow methods no Tube list calls.
 */
interface CreditsApi {

    @GET("v1/posts/genres")
    suspend fun genres(): ApiEnvelope<List<GenreDto>>

    @GET("v1/credits/requests")
    suspend fun requests(@Query("cursor") cursor: String? = null): ApiEnvelope<List<CreditRequestDto>>

    @POST("v1/credits/{creditId}/accept")
    suspend fun accept(@Path("creditId") creditId: String): ApiEnvelope<CreditStatusDto>

    @POST("v1/credits/{creditId}/decline")
    suspend fun decline(@Path("creditId") creditId: String): ApiEnvelope<CreditStatusDto>

    /** `{"status":"unlinked"}`; a repeat is 404, the link being gone already. */
    @DELETE("v1/credits/{creditId}")
    suspend fun removeMe(@Path("creditId") creditId: String): ApiEnvelope<CreditStatusDto>

    /**
     * The channel videos list's row shape — the post detail with its channel
     * card, and no `credits` — hydrated client-side like that list.
     */
    @GET("v1/creators/{creatorId}/appearances")
    suspend fun appearances(
        @Path("creatorId") creatorId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>
}
