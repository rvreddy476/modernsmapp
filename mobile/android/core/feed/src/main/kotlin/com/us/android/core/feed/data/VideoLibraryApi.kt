package com.us.android.core.feed.data

import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The long-video library (2026-10-02): Watch later, the private dislike and
 * the viewer's collections. post-service owns all of it, and every call here
 * is the one the web's watch page makes (`libraryApi.ts`, `watchApi.ts`,
 * `posttubeApi.ts`), path for path and body for body.
 *
 * Its own interface for the reason [VideoFeedApi] is: a fake of either would
 * otherwise grow members no test of it calls.
 */
// One route each, and they are one area of the server: splitting the interface would hide that.
@Suppress("TooManyFunctions")
interface VideoLibraryApi {

    /** Adds the post to the viewer's Watch later list. Idempotent: a second POST answers 200 again. */
    @POST("v1/posts/{postId}/watch-later")
    suspend fun addWatchLater(@Path("postId") postId: String): ApiEnvelope<WatchLaterDto>

    /** Removes it. Idempotent, and not gated on visibility: a post that went private can still be removed. */
    @DELETE("v1/posts/{postId}/watch-later")
    suspend fun removeWatchLater(@Path("postId") postId: String): ApiEnvelope<WatchLaterDto>

    /**
     * The viewer's PRIVATE dislike ("tune"). No count exists anywhere; the
     * server also removes a like made through its own toggle, and the
     * repository removes one made through `/reactions`.
     */
    @POST("v1/posts/{postId}/tune")
    suspend fun addDislike(@Path("postId") postId: String): ApiEnvelope<TuneDto>

    @DELETE("v1/posts/{postId}/tune")
    suspend fun removeDislike(@Path("postId") postId: String): ApiEnvelope<TuneDto>

    /**
     * A system collection by kind (`watch_later`, `liked`), created on first read.
     * Its items are read through [items] with the id this answers.
     */
    @GET("v1/playlists/system/{kind}")
    suspend fun systemPlaylist(@Path("kind") kind: String): ApiEnvelope<PlaylistDto>

    @GET("v1/playlists/{playlistId}")
    suspend fun playlist(@Path("playlistId") playlistId: String): ApiEnvelope<PlaylistDto>

    /** The rows in position order, each with its post already drawn (`post` is null for one the viewer may not see). */
    @GET("v1/playlists/{playlistId}/items")
    suspend fun items(@Path("playlistId") playlistId: String): ApiEnvelope<List<PlaylistItemDto>>

    /** One creator's collections; the viewer's own include the private ones. System lists are not among them. */
    @GET("v1/creators/{creatorId}/playlists")
    suspend fun creatorPlaylists(
        @Path("creatorId") creatorId: String,
        @Query("limit") limit: Int,
        @Query("offset") offset: Int,
    ): ApiEnvelope<List<PlaylistDto>>

    @POST("v1/playlists")
    suspend fun createPlaylist(@Body body: CreatePlaylistRequest): ApiEnvelope<PlaylistDto>

    /**
     * Adds a post at [AddPlaylistItemRequest.position]. The server UPSERTS on
     * `(playlist_id, position)`: a position already taken is OVERWRITTEN, so
     * the caller must send a free one. See [VideoLibraryRepository.addToCollection].
     */
    @POST("v1/playlists/{playlistId}/items")
    suspend fun addItem(
        @Path("playlistId") playlistId: String,
        @Body body: AddPlaylistItemRequest,
    ): ApiEnvelope<PlaylistItemDto>

    /** Answers 204 with no body, so it returns Unit and goes through `noContentApiCall`. */
    @DELETE("v1/playlists/{playlistId}/items/{postId}")
    suspend fun removeItem(
        @Path("playlistId") playlistId: String,
        @Path("postId") postId: String,
    )

    /**
     * `PATCH /v1/playlists/:id` (`UpdatePlaylist`, 2026-10-22 on Android): any
     * subset of title, description and visibility; a field left out (null,
     * which the app's Json omits) is left alone. The creator's own user
     * collections only: a system list answers `409 SYSTEM_PLAYLIST`, a blank
     * title or an unknown visibility `400`.
     */
    @PATCH("v1/playlists/{playlistId}")
    suspend fun updatePlaylist(
        @Path("playlistId") playlistId: String,
        @Body body: UpdatePlaylistRequest,
    ): ApiEnvelope<PlaylistDto>

    /** `DELETE /v1/playlists/:id`: 204, no body. The videos in it are not touched. */
    @DELETE("v1/playlists/{playlistId}")
    suspend fun deletePlaylist(@Path("playlistId") playlistId: String)

    /**
     * `PATCH /v1/playlists/:id/items/:postId {position}` (`MovePlaylistItem`),
     * the reorder the web's drag and Move up / Move down use. [MovePlaylistItemRequest.position]
     * is the 0-based index the post should land on; the server clamps it,
     * re-writes the whole order and answers the items in their new order,
     * renumbered from 0 — bare rows, without `post`.
     */
    @PATCH("v1/playlists/{playlistId}/items/{postId}")
    suspend fun moveItem(
        @Path("playlistId") playlistId: String,
        @Path("postId") postId: String,
        @Body body: MovePlaylistItemRequest,
    ): ApiEnvelope<List<PlaylistItemDto>>

    companion object {
        /** post-service's kind for Watch later. */
        const val KIND_WATCH_LATER = "watch_later"

        /** The viewer's liked long videos ("Liked videos"), written by the server's like toggle alone. */
        const val KIND_LIKED = "liked"

        /** A collection the viewer made. */
        const val KIND_USER = "user"
    }
}

/**
 * The PATCH body. Every field nullable and defaulted to null, so only what
 * changed goes on the wire (the app's Json omits nulls and defaults).
 */
@Serializable
data class UpdatePlaylistRequest(
    val title: String? = null,
    val description: String? = null,
    val visibility: String? = null,
)

/** `{position}`: always sent, 0 included (the server reads an absent one as a 400). No default for that reason. */
@Serializable
data class MovePlaylistItemRequest(val position: Int)

/** `{"queued":true|false}`: the state after the call. */
@Serializable
data class WatchLaterDto(val queued: Boolean = false)

/** `{"ok":true}`. Nothing here is rendered; a 2xx is the signal. */
@Serializable
data class TuneDto(val ok: Boolean = false)

/** A collection as post-service sends it: the row, never its items. */
@Serializable
data class PlaylistDto(
    val id: String = "",
    @SerialName("creator_id") val creatorId: String = "",
    @SerialName("channel_id") val channelId: String? = null,
    val title: String = "",
    val description: String = "",
    @SerialName("cover_url") val coverUrl: String? = null,
    val visibility: String = "",
    @SerialName("item_count") val itemCount: Int = 0,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    /** `user` for one the viewer made, or a system kind (`watch_later`, `liked`). */
    val kind: String = "",
)

/** One row of a collection. [post] is the bare `PostDetail`, hydrated client-side like any post-service row. */
@Serializable
data class PlaylistItemDto(
    @SerialName("playlist_id") val playlistId: String = "",
    @SerialName("post_id") val postId: String = "",
    val position: Int = 0,
    @SerialName("added_at") val addedAt: String = "",
    val post: FeedItemDto? = null,
)

/** The web's body: a title and a visibility. One made from the watch page is private unless chosen otherwise. */
@Serializable
data class CreatePlaylistRequest(
    val title: String,
    val visibility: String,
)

@Serializable
data class AddPlaylistItemRequest(
    @SerialName("post_id") val postId: String,
    val position: Int,
)
