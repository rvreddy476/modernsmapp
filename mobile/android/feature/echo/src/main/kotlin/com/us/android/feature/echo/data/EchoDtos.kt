package com.us.android.feature.echo.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The wire shapes of docs/designs/echo-contract.md and echo-v2-contract.md,
 * pinned by the golden fixtures copied byte for byte from post-service
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
    // Echo v2 (echo-v2-contract.md section 1).
    @SerialName("view_count") val viewCount: Long = 0,
    @SerialName("bookmark_count") val bookmarkCount: Long = 0,
    @SerialName("viewer_has_bookmarked") val viewerHasBookmarked: Boolean = false,
    @SerialName("quote_count") val quoteCount: Long = 0,
    val status: String = "",
    @SerialName("scheduled_at") val scheduledAt: String? = null,
    val poll: PollDto? = null,
    @SerialName("publish_error") val publishError: String? = null,
    @SerialName("thread_parts") val threadParts: Int = 0,
    // Video in notes (echo-v2-contract.md "Video in notes", 15 Nov 2026).
    @SerialName("post_type") val postType: String = "",
    /** True until every asset is ready and passed; only the author is shown the note meanwhile. */
    @SerialName("is_processing") val isProcessing: Boolean = false,
    /** The author's own note whose video is longer than the note allows; omitted when false. */
    @SerialName("video_too_long") val videoTooLong: Boolean = false,
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

/** One existing post media item: an image, a GIF, or (15 Nov 2026) one video. */
@Serializable
data class NoteMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    @SerialName("alt_text") val altText: String = "",
    val position: Int = 0,
    @SerialName("processing_status") val processingStatus: String = "",
    /** A video's length once measured (usually after transcoding); omitted while unknown. */
    @SerialName("duration_ms") val durationMs: Long = 0,
    /** Gateway-relative master playlist, once the transcode produced one. */
    @SerialName("hls_url") val hlsUrl: String? = null,
)

/** A note's poll. Each `votes` is null while counts are withheld from this viewer. */
@Serializable
data class PollDto(
    val options: List<PollOptionDto>? = null,
    @SerialName("total_votes") val totalVotes: Long = 0,
    @SerialName("ends_at") val endsAt: String = "",
    val closed: Boolean = false,
    @SerialName("viewer_vote") val viewerVote: Int? = null,
)

@Serializable
data class PollOptionDto(
    val index: Int = 0,
    val label: String = "",
    val votes: Long? = null,
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

/**
 * `POST /v1/notes`. [replyToId] and [quoteOfId] are mutually exclusive
 * (400 INVALID_REQUEST); a [poll] note carries no media (400
 * POLL_WITH_MEDIA); a reply is never scheduled.
 */
@Serializable
data class CreateNoteRequest(
    val text: String,
    @SerialName("media_ids") val mediaIds: List<String>? = null,
    @SerialName("reply_to_id") val replyToId: String? = null,
    @SerialName("quote_of_id") val quoteOfId: String? = null,
    @SerialName("reply_policy") val replyPolicy: String? = null,
    val poll: CreatePollRequest? = null,
    @SerialName("scheduled_at") val scheduledAt: String? = null,
)

/** `poll` on `POST /v1/notes`: 2 to 4 options of up to 25 characters, open 5 minutes to 7 days. */
@Serializable
data class CreatePollRequest(
    val options: List<String>,
    @SerialName("duration_minutes") val durationMinutes: Int,
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

/** `POST /v1/notes/:id/poll/vote`. No Kotlin default, so option 0 is still sent. */
@Serializable
data class VoteRequest(@SerialName("option_index") val optionIndex: Int)

/** `PATCH /v1/notes/:id/schedule`. */
@Serializable
data class ScheduleRequest(@SerialName("scheduled_at") val scheduledAt: String)

/** `POST|DELETE /v1/notes/:id/bookmark` answers the viewer's state and the public count. */
@Serializable
data class BookmarkDto(
    val bookmarked: Boolean = false,
    val count: Long = 0,
)

/** `POST /v1/notes/views`: up to 50 ids. */
@Serializable
data class ViewsRequest(@SerialName("note_ids") val noteIds: List<String>)

/** `GET /v1/notes/trends`. */
@Serializable
data class TrendListDto(val items: List<TrendDto>? = null)

@Serializable
data class TrendDto(
    val tag: String = "",
    @SerialName("note_count") val noteCount: Long = 0,
    val rank: Int = 0,
)
