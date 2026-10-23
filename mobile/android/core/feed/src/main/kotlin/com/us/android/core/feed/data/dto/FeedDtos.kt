package com.us.android.core.feed.data.dto

import com.us.android.core.feed.data.credits.CreditDto
import com.us.android.core.feed.data.credits.CreditListSerializer
import com.us.android.core.feed.data.credits.GenreSlugListSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * Wire types for feed-service, transcribed from the 2026-08-17 captures
 * (prompt/android-api-contracts.md §1 and the evidence-pass section).
 *
 * Every field has a default. That is not habit: `score` is absent on home and
 * present on ranked surfaces, `media` is absent on a text post rather than an
 * empty array, `hls_url` is absent for an image, and `viewer_reaction` is
 * omitted entirely when unauthenticated or unset. A parser that required any
 * of them would fail on half the real fixtures.
 */

/** A hydrated feed item — renders a whole row with no follow-up call. */
@Serializable
data class FeedItemDto(
    val id: String = "",
    @SerialName("author_id") val authorId: String = "",
    val author: FeedAuthorDto = FeedAuthorDto(),
    val text: String = "",
    /** The long video's title (Tube, 2026-09-05); `omitempty`, so absent elsewhere. */
    val title: String = "",
    val visibility: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("feed_content_type") val feedContentType: String = "",
    @SerialName("post_type") val postType: String = "",
    @SerialName("app_origin") val appOrigin: String = "",
    @SerialName("is_pinned") val isPinned: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val media: List<FeedMediaDto> = emptyList(),
    val counts: FeedCountsDto = FeedCountsDto(),
    @SerialName("view_count") val viewCount: Int = 0,
    @SerialName("repost_count") val repostCount: Int = 0,
    @SerialName("is_bookmarked") val isBookmarked: Boolean = false,
    @SerialName("has_reacted") val hasReacted: Boolean = false,
    @SerialName("has_reposted") val hasReposted: Boolean = false,
    @SerialName("viewer_reaction") val viewerReaction: String? = null,
    /**
     * The viewer's Watch later membership and private dislike (MTube,
     * 2026-09-27): never omitted on the post detail, absent on list rows of
     * a server that does not load them, so both default to false.
     */
    @SerialName("viewer_queued") val viewerQueued: Boolean = false,
    @SerialName("viewer_disliked") val viewerDisliked: Boolean = false,
    @SerialName("is_repostable") val isRepostable: Boolean = false,
    /** Ranked surfaces only. Home is chronological and omits it. */
    val score: Double? = null,
    /** Present exactly when the post is a poll — hydrated by feed-service. */
    val poll: FeedPollDto? = null,
    /**
     * Instant reels (2026-09-04): the video is still transcoding or being
     * moderated. The author sees the row now; others see it when this clears.
     */
    @SerialName("is_processing") val isProcessing: Boolean = false,
    // The author's per-post controls, as the reel form sent them.
    @SerialName("no_comments") val noComments: Boolean = false,
    @SerialName("hide_share") val hideShare: Boolean = false,
    @SerialName("allow_download") val allowDownload: Boolean = true,
    @SerialName("remix_setting") val remixSetting: String = "",
    val category: String = "",
    @SerialName("tagged_user_ids") val taggedUserIds: List<String> = emptyList(),
    @SerialName("location_name") val locationName: String = "",
    @SerialName("cover_media_id") val coverMediaId: String? = null,
    /**
     * The channel a long video was posted under (Tube, 2026-09-05). Absent on
     * every other kind of post and on a server without channels.
     */
    val channel: FeedChannelDto? = null,
    /**
     * "Why you're seeing this post" — feed-service reason.go. A stable token
     * and the sentence to show; both `omitempty`, so absent on the viewer's
     * own posts and on older servers.
     */
    val reason: String = "",
    @SerialName("reason_text") val reasonText: String = "",
    /**
     * Soft delete (founder, 2026-09-04): set once the author deleted the
     * post, blank otherwise (`omitempty`). A row that arrives with it set is
     * dropped before it is mapped — see `toFeedPage` in FeedPagingSource —
     * — so a feed that still carries a deleted post never renders it.
     */
    @SerialName("deleted_at") val deletedAt: String = "",
    /** The author's hashtag chips and mentions (2026-09-05); `omitempty` on older rows. */
    val hashtags: List<String> = emptyList(),
    val mentions: List<String> = emptyList(),
    /** A scheduled post's instant and flag (2026-09-05); absent on a post that went out at once. */
    @SerialName("publish_at") val publishAt: String? = null,
    @SerialName("is_scheduled") val isScheduled: Boolean = false,
    /**
     * Original sounds (2026-09-30, contract 2.1). `audio_track_id` stays on
     * the wire even when [sound] is withheld from this viewer, so the id alone
     * is NOT a sound to play: only [sound] is.
     */
    @SerialName("audio_track_id") val audioTrackId: String = "",
    @SerialName("audio_start_ms") val audioStartMs: Long = 0L,
    /** Omitted when the post plays no added sound, or this viewer may not hear it. */
    val sound: FeedSoundDto? = null,
    /**
     * The creator's two levels. Nullable, never defaulted to a number: absent
     * is 1 and a PRESENT 0 is a real 0 (the creator muted that side), and a
     * default of either would make the two indistinguishable.
     */
    @SerialName("original_audio_volume") val originalAudioVolume: Double? = null,
    @SerialName("overlay_audio_volume") val overlayAudioVolume: Double? = null,
    /**
     * What the author declared about the post (post-screens parity,
     * 2026-10-18; feed-service passes them through). Every one defaulted, so
     * a row from a server that predates them reads as "nothing declared".
     */
    @SerialName("paid_promotion") val paidPromotion: Boolean = false,
    @SerialName("is_branded") val isBranded: Boolean = false,
    @SerialName("ai_generated") val aiGenerated: Boolean = false,
    @SerialName("link_url") val linkUrl: String? = null,
    /** `everyone`, `followers` or `nobody`; blank on an older row. */
    @SerialName("comment_access") val commentAccess: String = "",
    /**
     * A long video's cast & crew and film facts (video credits, 2026-10-23).
     * `credits` is ONLY on the post detail (`GET /v1/posts/:id`) and the
     * create / PATCH answers — never on a feed, list or appearances row;
     * `genres`, `release_year` and `production_house` are on every post body,
     * left off when unset. Anything but an array reads as none.
     */
    @Serializable(with = CreditListSerializer::class)
    val credits: List<CreditDto> = emptyList(),
    @Serializable(with = GenreSlugListSerializer::class)
    val genres: List<String> = emptyList(),
    @SerialName("release_year") val releaseYear: Int? = null,
    @SerialName("production_house") val productionHouse: String? = null,
    /**
     * Members-only posts (paid memberships, 2026-10-23). `tier_required_id`
     * is on every gated row, member or not (post-service posts.go:131,
     * feed-service hydration.go:177); `body_redacted` is true only where the
     * server emptied the body for this viewer (posts.go:132, hydration.go:178);
     * `duration_ms` rides only on a redacted row (posts.go:137,
     * hydration.go:182). All `omitempty`, so an open post carries none.
     */
    @SerialName("tier_required_id") val tierRequiredId: String? = null,
    @SerialName("body_redacted") val bodyRedacted: Boolean = false,
    @SerialName("duration_ms") val durationMs: Long = 0L,
    /** The level's monthly price on a gated row (post-service posts.go:144); absent elsewhere. */
    @SerialName("tier_required_price_paise") val tierRequiredPricePaise: Long? = null,
    /**
     * The Action button (2026-10-23): `{id, label, label_text, domain, href}`
     * when present and live; the author's rows add `url` and `status`. Raw,
     * read by `toPostAction`, so absent, `null` or a stray shape is no button.
     */
    val action: JsonElement? = null,
    /**
     * Copyright title review (2026-10-24): `{state, matched_title, created_at,
     * decided_at?, review_requested_at?}` on the AUTHOR's own post detail of a
     * flagged long video (post-service service/post.go:615); absent on every
     * other row. Raw, read by `toCopyrightReview`.
     */
    @SerialName("copyright_review") val copyrightReview: JsonElement? = null,
)

/**
 * The added sound a post plays — post-service's `PostSound`, the eight keys
 * of contract 2.1, passed through feed-service unchanged. Go sends zero
 * values where a value is unset; `toReelSound` reads them as absent.
 */
@Serializable
data class FeedSoundDto(
    val id: String = "",
    val title: String = "",
    val artist: String = "",
    @SerialName("duration_ms") val durationMs: Long = 0L,
    @SerialName("start_ms") val startMs: Long = 0L,
    @SerialName("use_count") val useCount: Long = 0L,
    @SerialName("source_post_id") val sourcePostId: String? = null,
    @SerialName("creator_user_id") val creatorUserId: String? = null,
)

/**
 * The hydrated poll block — post-service's `PollData`, passed through
 * feed-service as raw JSON. Server-computed percentages included; the client
 * renders them, never derives them.
 */
@Serializable
data class FeedPollDto(
    val question: String = "",
    @SerialName("allows_multiple") val allowsMultiple: Boolean = false,
    val options: List<FeedPollOptionDto> = emptyList(),
    @SerialName("total_votes") val totalVotes: Long = 0,
    @SerialName("viewer_votes") val viewerVotes: List<String> = emptyList(),
    @SerialName("has_ended") val hasEnded: Boolean = false,
)

@Serializable
data class FeedPollOptionDto(
    val id: String = "",
    val label: String = "",
    @SerialName("vote_count") val voteCount: Long = 0,
    val percentage: Double = 0.0,
)

/**
 * Embedded author.
 *
 * Defaults to an empty object rather than being nullable: the server always
 * sends `author`, and substituting a non-enumerating placeholder for a deleted
 * profile is its job, not the client's.
 */
@Serializable
data class FeedAuthorDto(
    val id: String = "",
    @SerialName("display_name") val displayName: String = "",
    val username: String? = null,
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
)

/**
 * A media reference with its authorized delivery fields.
 *
 * `variants` values are absolute pre-signed URLs; `hls_url` is a
 * gateway-relative authorized path. They are NOT interchangeable — see
 * [com.us.android.core.model.FeedMedia].
 */
@Serializable
data class FeedMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    /** The carousel ordinal. Defaults to -1, never 0 — see CarouselOrdinals. */
    val position: Int = com.us.android.core.model.CarouselOrdinals.ABSENT,
    /**
     * The author's accessibility decision — Slice C, C-CLB-3.
     *
     * Defaulted for the same reason as [com.us.android.feature.post.data.dto.PostMediaDto]:
     * older posts do not carry these and must still decode.
     */
    @SerialName("alt_text") val altText: String = "",
    @SerialName("alt_decorative") val altDecorative: Boolean = false,
    val status: String = "",
    val width: Int = 0,
    val height: Int = 0,
    val blurhash: String = "",
    val variants: Map<String, String> = emptyMap(),
    @SerialName("hls_url") val hlsUrl: String? = null,
    /** Five minutes after issue, per the captured contract. */
    @SerialName("expires_at") val expiresAt: String? = null,
    // Instant reels: per-media processing, and the server's own choice of
    // what to play — the HLS master once it exists, the original file until.
    @SerialName("processing_status") val processingStatus: String = "",
    @SerialName("moderation_status") val moderationStatus: String = "",
    @SerialName("playback_url") val playbackUrl: String? = null,
    @SerialName("playback_kind") val playbackKind: String = "",
    /** The transcode's measured length (Tube, 2026-09-05); 0 for images and older rows. */
    @SerialName("duration_ms") val durationMs: Long = 0L,
)

@Serializable
data class FeedCountsDto(
    val likes: Int = 0,
    val comments: Int = 0,
    /**
     * Sent by some rows and not by others (post-service's listing carries
     * likes and comments only), so absent stays absent: the reel rail shows a
     * count only when the server gave one. `bookmarks` is the older spelling
     * of `saves`.
     */
    val shares: Int? = null,
    val saves: Int? = null,
    val bookmarks: Int? = null,
)

/**
 * `GET /v1/feed/delta`.
 *
 * Not a list request: `feed_type` and `anchor` are required, and omitting
 * `feed_type` returns `INVALID_REQUEST`. No cursor envelope — `has_more` lives
 * inside `data`, and `newest_anchor` is what the client retains.
 */
@Serializable
data class FeedDeltaDto(
    @SerialName("new_count") val newCount: Int = 0,
    @SerialName("newest_anchor") val newestAnchor: String = "",
    @SerialName("has_more") val hasMore: Boolean = false,
)

/**
 * `GET /v1/hashtags/trending` — the `data` object.
 *
 * Captured from the dev gateway on 2026-09-04: `{"data":{"hashtags":[]}}`.
 * The list is keyed, not top-level, so this wrapper exists; a non-empty day
 * fills each entry from post-service's `HashtagTrending24h`
 * (`normalized_name`, `display_name`, `post_count`).
 */
@Serializable
data class TrendingHashtagsDto(
    val hashtags: List<TrendingHashtagDto> = emptyList(),
)

@Serializable
data class TrendingHashtagDto(
    /** The tag without `#`, as the posts-by-tag path takes it. */
    @SerialName("normalized_name") val normalizedName: String = "",
    /** `#tag` — what the server wants shown. */
    @SerialName("display_name") val displayName: String = "",
    @SerialName("post_count") val postCount: Long = 0,
)

/**
 * The channel embedded on a long-video row — `{user_id, name, handle,
 * avatar_url}` (Tube channels, 2026-09-05). `avatar_url` is a delivery URL
 * the server already resolved, used as returned.
 */
@Serializable
data class FeedChannelDto(
    @SerialName("user_id") val userId: String = "",
    val name: String = "",
    val handle: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
)
