package com.us.android.core.creator.model

import kotlinx.serialization.Serializable

/**
 * What the post details screen set (post-screens parity, 2026-10-18): the
 * title, tags, audience, the comment and reuse switches, the disclosures, a
 * link, a place and which photo is the cover.
 *
 * ## WHY THIS IS NOT IN THE PROJECT DOCUMENT
 *
 * [AndroidCreatorProject] is a frozen contract whose canonical bytes the
 * fixtures pin by SHA-256, and the canonicalizer encodes every default — a
 * new field there would change every stored document's fingerprint. These
 * choices are about the POST, not the pages, so they travel beside the
 * document: in the publish job's input, and into the request when it is
 * frozen ([PublishTransport.freezeCreateRequest]).
 *
 * Every field is nullable and null means "not set": a publish with no
 * details freezes byte-for-byte the request it froze before this existed.
 */
@Serializable
data class PostDetails(
    val title: String? = null,
    /** Without `#`. */
    val hashtags: List<String>? = null,
    /** Usernames, without `@`. */
    val mentions: List<String>? = null,
    /** `public`, `friends` or `private`. */
    val visibility: String? = null,
    /** The "Allow comments" switch. */
    val allowComments: Boolean? = null,
    /** The "Allow reuse of content" switch. */
    val allowReuse: Boolean? = null,
    val paidPromotion: Boolean? = null,
    val isBranded: Boolean? = null,
    val aiGenerated: Boolean? = null,
    /** An https link, already validated. */
    val linkUrl: String? = null,
    val locationName: String? = null,
    /** The place provider's id for [locationName], when the place came from search. */
    val locationPlaceId: String? = null,
    /** Which page is the cover, 0-based, on a photo post. */
    val coverIndex: Int? = null,
)
