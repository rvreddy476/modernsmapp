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
const val CONTENT_TYPE_POST = "post"
const val POST_TYPE_TEXT = "text"
const val POST_TYPE_IMAGE = "image"
const val APP_ORIGIN_POSTBOOK = "postbook"
const val DISTRIBUTION_VERSION = 1

/**
 * The audiences this client may offer, as a set the tests own.
 *
 * A single-element allowlist rather than a hardcoded string, so adding an
 * option is a visible change to a guarded constant rather than an edit buried
 * in a dropdown. `AudienceContractTest` fails the build if anything joins it
 * without the end-to-end authorization tests that would justify it.
 */
val SupportedAudience: Set<String> = setOf(VISIBILITY_PUBLIC)
