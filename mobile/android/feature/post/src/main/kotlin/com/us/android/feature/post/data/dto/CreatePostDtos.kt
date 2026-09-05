package com.us.android.feature.post.data.dto

import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The Slice C create-post request.
 *
 * ## THIS IS A DELIBERATELY SMALL SUBSET
 *
 * `CreatePostRequest` on the server carries roughly forty fields — polls,
 * threads, location, feeling, tags, licensing, monetization, reel metadata.
 * Slice C sends nine. Everything absent here is absent because it is out of
 * scope, not because it was forgotten: an omitted field cannot create a control
 * the platform does not enforce end to end.
 *
 * ## EVERY REQUIRED FIELD IS EXPLICITLY ENCODED
 *
 * `@EncodeDefault(ALWAYS)` is not decoration. kotlinx.serialization omits any
 * property equal to its default, and the app's shared `Json` leaves
 * `encodeDefaults` off — deliberately, and it must stay off, because turning it
 * on globally would start emitting every default in every request across the
 * app and change contracts nobody re-tested.
 *
 * This project has already shipped that defect twice: `SendMessageRequest.type`
 * made every chat send a 400, and `ResendVerificationRequestDto.type` broke the
 * account-recovery path. `visibility` here is `binding:"required"` server-side,
 * so the same omission would make every post fail.
 *
 * `CreatePostWireTest` asserts the literal bytes, and `NC-C6A` proves the
 * annotation is load-bearing by removing it.
 */
@Serializable
data class CreatePostRequest(
    /**
     * The post body. May be empty ONLY when a media id is present — the
     * non-empty invariant is enforced by the composer and again by the server,
     * because a hostile client will not enforce it for us.
     */
    val text: String,

    /**
     * ALWAYS `"public"` in Slice C, and always on the wire.
     *
     * Not a Kotlin default that could vanish: the server binds this
     * `required,oneof=public followers private unlisted`, so an omitted value is
     * a 400 on every publish.
     *
     * The value is `public` because it is the only audience the platform
     * currently enforces end to end. `followers`, `private` and `unlisted` are
     * accepted by the create handler but are not applied by the post read path,
     * the profile list, or feed fan-out — so offering them would record a
     * privacy choice that nothing honours. See `SupportedAudience`.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val visibility: String = VISIBILITY_PUBLIC,

    /**
     * The canonical content type. `post` for everything in this slice.
     *
     * Distinct from [postType]: `content_type` is the storage/product family
     * (post, poll, flick, long_video) and `post_type` is the legacy shape hint.
     * Slice C adds no new semantics to either.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("content_type") val contentType: String = CONTENT_TYPE_POST,

    /** `text` or `image` — the legacy shape hint, mirroring what was attached. */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("post_type") val postType: String = POST_TYPE_TEXT,

    /** Which surface authored this. Fixed for the social composer. */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("app_origin") val appOrigin: String = APP_ORIGIN_POSTBOOK,

    /**
     * Zero or one confirmed, ready media id.
     *
     * Always emitted, including as `[]`: an omitted array and an empty one
     * should mean the same thing, but relying on that is relying on server
     * defaulting the client cannot see.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("media_ids") val mediaIds: List<String> = emptyList(),

    /**
     * BCP-47 language, chosen by the creator.
     *
     * Recorded, never detected. Calling a guess "auto-detected" is how a
     * mislabelled post becomes unfindable in its own language, and Slice C
     * explicitly stores an explicit choice only.
     */
    val language: String,

    /**
     * The typed distribution policy. MANDATORY, not optional.
     *
     * Without it the server falls back to legacy behaviour whose
     * `notify_subscribers` default is `true`, so an ordinary social post would
     * notify every subscriber of a channel the author may not even have meant
     * to post to. Sending the policy explicitly is what makes a normal post
     * normal.
     */
    val distribution: DistributionRequest,

    /**
     * Present exactly when [contentType] is `poll`; omitted from the wire
     * otherwise (null default, `encodeDefaults` off), so every existing
     * request — including Studio's FROZEN replay bytes — is byte-identical to
     * what it was before this field existed.
     */
    val poll: CreatePollRequest? = null,

    /**
     * Reel title. Present exactly when [contentType] is `flick`; omitted
     * otherwise, for the same byte-stability reason as [poll].
     *
     * The reel form has no title field (founder, 2026-09-04): the caption is
     * the only text, so a reel sends `""` and relies on the server's relaxed
     * title requirement.
     */
    val title: String? = null,

    // ── Reel-only fields ────────────────────────────────────────────────
    //
    // Every one is nullable with a null default, so a request that does not
    // set it is byte-identical to the request before the field existed —
    // Studio's frozen replay bytes and the wire tests depend on that. The
    // reel form sets all four switches EXPLICITLY, including their defaults:
    // `allow_download` omitted is "unspecified", not "true".

    /** The "Allow comments" switch, inverted: `true` turns comments off. */
    @SerialName("no_comments") val noComments: Boolean? = null,

    /** The "Hide share button" switch. */
    @SerialName("hide_share") val hideShare: Boolean? = null,

    /** The "Allow download" switch. */
    @SerialName("allow_download") val allowDownload: Boolean? = null,

    /**
     * The "Allow remix" switch, as the server's `remix_setting`:
     * [REMIX_ALLOW] or [REMIX_DISALLOW]. There is no `allow_remix` bool.
     */
    @SerialName("remix_setting") val remixSetting: String? = null,

    /** A category id from `GET /v1/posts/categories`; omitted when "None". */
    val category: String? = null,

    /**
     * The chosen cover frame, uploaded as an IMAGE through the same path the
     * composer uses for a photo and confirmed ready+passed before this is set.
     */
    @SerialName("cover_media_id") val coverMediaId: String? = null,

    /** People tagged on the reel, by user id. Omitted when nobody is tagged. */
    @SerialName("tagged_user_ids") val taggedUserIds: List<String>? = null,

    /** A typed place name. No coordinates: this pass has no maps SDK. */
    @SerialName("location_name") val locationName: String? = null,

    // ── The studio's details step (2026-09-05) ─────────────────────────
    //
    // Nullable with a null default, like the reel fields above: a request
    // that sets none of them is byte-identical to the request before they
    // existed.

    /** The hashtag chips, without `#`, at most thirty. Never mixed into [text]. */
    val hashtags: List<String>? = null,

    /** The mentioned people's usernames, without `@`, at most twenty. */
    val mentions: List<String>? = null,

    /**
     * RFC 3339 instant the post goes live — five minutes to thirty days
     * ahead. The server answers `is_scheduled` and holds the post until
     * then; a value it refuses comes back as a 400 in its own words.
     */
    @SerialName("publish_at") val publishAt: String? = null,
)

/**
 * The server's `CreatePollRequest`: a question and 2–6 options
 * (`binding:"required,min=2,max=6"`), optional multi-select, optional
 * duration in hours (server default applies when omitted).
 */
@Serializable
data class CreatePollRequest(
    val question: String,
    val options: List<String>,
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("allows_multiple") val allowsMultiple: Boolean = false,
    @SerialName("duration_hours") val durationHours: Int? = null,
)

/**
 * Version-1 distribution policy.
 *
 * Every field is explicitly encoded, including the `false` ones. That is the
 * entire point: `notify_subscribers = false` omitted from the wire is not
 * "false", it is "unspecified", and unspecified means the legacy `true`.
 */
@Serializable
data class DistributionRequest(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val version: Int = DISTRIBUTION_VERSION,

    /** A social post goes to the main feed. */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("main_feed") val mainFeed: Boolean = true,

    /** Explicitly false — see the class doc. */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("notify_subscribers") val notifySubscribers: Boolean = false,

    /**
     * Explicitly false. PostTube/Reels is a separate product with its own
     * canonical video record; a text-or-photo post must never create one.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("create_reel_preview") val createReelPreview: Boolean = false,
)

const val VISIBILITY_PUBLIC = "public"
const val VISIBILITY_FOLLOWERS = "followers"
const val VISIBILITY_PRIVATE = "private"
const val CONTENT_TYPE_POST = "post"
const val CONTENT_TYPE_POLL = "poll"
const val CONTENT_TYPE_FLICK = "flick"

/**
 * A long video — Tube (2026-09-05). The same create call as a flick with a
 * required `title` and no `remix_setting`; post-service keeps it out of Reels
 * and puts it on `GET /v1/feed/videos`.
 */
const val CONTENT_TYPE_LONG_VIDEO = "long_video"

/** `remix_setting` values — the only two the create handler accepts. */
const val REMIX_ALLOW = "allow"
const val REMIX_DISALLOW = "disallow"

/**
 * A voice post — `post-service/internal/service/post.go:562`
 * (`validContentTypes` carries `"voice"`). One audio media id plus optional
 * text; the server re-derives the type from the attached asset's kind and
 * holds the post out of public surfaces until audio safety passes.
 */
const val CONTENT_TYPE_VOICE = "voice"
const val POST_TYPE_TEXT = "text"
const val POST_TYPE_IMAGE = "image"
const val POST_TYPE_VIDEO = "video"

/** The legacy shape hint for a voice post: what is attached is audio. */
const val POST_TYPE_AUDIO = "audio"
const val APP_ORIGIN_POSTBOOK = "postbook"
const val DISTRIBUTION_VERSION = 1

/**
 * The audiences this client may offer, as a set the tests own.
 *
 * An allowlist rather than hardcoded strings, so adding an option is a
 * visible change to a guarded constant rather than an edit buried in a
 * dropdown. `followers` and `private` joined 2026-09-01, when post-service
 * grew the read-path enforcement (direct-link gate, profile-grid filter)
 * that the engagement, feed-batch and repost paths already had — before
 * that, offering them would have recorded a promise nothing kept.
 * `unlisted` stays out: no surface explains it.
 */
val SupportedAudience: Set<String> =
    setOf(VISIBILITY_PUBLIC, VISIBILITY_FOLLOWERS, VISIBILITY_PRIVATE)
