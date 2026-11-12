package com.us.android.feature.echo.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The wire shapes of docs/designs/echo-contract.md, pinned by the golden
 * fixtures copied byte for byte from post-service
 * (`testdata/contracts/notes/`). A note is the post JSON with the Echo
 * fields always present; only what the screens read is declared here, and
 * the app's Json ignores the rest.
 *
 * Requests carry no Kotlin defaults that matter: the app's Json leaves
 * `encodeDefaults` off and `explicitNulls` off, so an absent optional is
 * simply not sent.
 */
@Serializable
data class NoteDto(
    val id: String = "",
    @SerialName("author_id") val authorId: String = "",
    @SerialName("content_type") val contentType: String = "",
    val text: String = "",
    val media: List<NoteMediaDto>? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("edited_at") val editedAt: String? = null,
    @SerialName("reply_to_id") val replyToId: String? = null,
    @SerialName("conversation_id") val conversationId: String? = null,
    @SerialName("quote_of_id") val quoteOfId: String? = null,
    @SerialName("reply_policy") val replyPolicy: String = "",
    @SerialName("reply_count") val replyCount: Long = 0,
    @SerialName("like_count") val likeCount: Long = 0,
    @SerialName("repost_count") val repostCount: Long = 0,
    @SerialName("viewer_has_liked") val viewerHasLiked: Boolean = false,
    @SerialName("viewer_has_reposted") val viewerHasReposted: Boolean = false,
    @SerialName("viewer_can_reply") val viewerCanReply: Boolean = false,
    val author: NoteAuthorDto? = null,
    @SerialName("quoted_note") val quotedNote: NoteDto? = null,
)

@Serializable
data class NoteAuthorDto(
    val id: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("is_verified") val isVerified: Boolean = false,
    @SerialName("verification_level") val verificationLevel: String = "",
    @SerialName("badge_flags") val badgeFlags: Int = 0,
)

/** One existing post media item. Notes carry images and GIFs only. */
@Serializable
data class NoteMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    @SerialName("alt_text") val altText: String = "",
    val position: Int = 0,
    @SerialName("processing_status") val processingStatus: String = "",
)

/** `{items:[Note], next_cursor}`: the timelines, replies and a profile's tab. The cursor rides in `data`. */
@Serializable
data class NoteListDto(
    val items: List<NoteDto>? = null,
    @SerialName("next_cursor") val nextCursor: String? = null,
)

/** `POST /v1/notes/thread` answers `{notes:[Note]}`. */
@Serializable
data class NoteThreadDto(
    val notes: List<NoteDto>? = null,
)

/** `GET /v1/notes/me/eligibility`. */
@Serializable
data class EligibilityDto(
    @SerialName("can_post") val canPost: Boolean = false,
    @SerialName("can_reply") val canReply: Boolean = false,
    val reason: String = "",
)

/** `POST /v1/notes`. [replyToId] and [quoteOfId] are mutually exclusive (400 INVALID_REQUEST). */
@Serializable
data class CreateNoteRequest(
    val text: String,
    @SerialName("media_ids") val mediaIds: List<String>? = null,
    @SerialName("reply_to_id") val replyToId: String? = null,
    @SerialName("quote_of_id") val quoteOfId: String? = null,
    @SerialName("reply_policy") val replyPolicy: String? = null,
)

@Serializable
data class ThreadEntryRequest(
    val text: String,
    @SerialName("media_ids") val mediaIds: List<String>? = null,
)

/** `POST /v1/notes/thread`: 2–25 entries. */
@Serializable
data class CreateThreadRequest(
    val entries: List<ThreadEntryRequest>,
    @SerialName("reply_policy") val replyPolicy: String? = null,
)

/** `PATCH /v1/notes/:id`: the text within 30 minutes, the reply policy any time. */
@Serializable
data class EditNoteRequest(
    val text: String? = null,
    @SerialName("reply_policy") val replyPolicy: String? = null,
)
