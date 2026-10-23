package com.us.android.feature.post.studio

import com.us.android.core.creator.model.PostDetails
import com.us.android.core.feed.data.action.toWriteDto
import com.us.android.core.model.ActionDraft
import com.us.android.feature.post.data.dto.COMMENT_ACCESS_EVERYONE
import com.us.android.feature.post.data.dto.COMMENT_ACCESS_NOBODY
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_IMAGE
import com.us.android.feature.post.data.dto.REMIX_ALLOW
import com.us.android.feature.post.data.dto.REMIX_DISALLOW
import com.us.android.feature.post.data.dto.SupportedAudience
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import java.net.URI

/**
 * The Studio's create request: the pages' media and the caption, plus what
 * the details screen set (2026-10-18). Pure, so every mapping is a table
 * test and the frozen bytes come from one place.
 *
 * Every detail left unset stays off the wire ([PostDetails] is all nulls by
 * default), so a publish with no details is byte-identical to the request
 * this froze before the details screen existed.
 *
 *  - Allow comments → `no_comments` (inverted) AND `comment_access`
 *    (`everyone` / `nobody`): the older server reads the first, the current
 *    one enforces the second.
 *  - Allow reuse → `remix_setting` `allow` / `disallow`.
 *  - The cover is a PAGE of the post: `cover_media_id` is the media id at
 *    [PostDetails.coverIndex], on a photo post only; an index past the pages
 *    sends no cover rather than a guess.
 *  - An audience outside [SupportedAudience] falls back to public, the same
 *    rule the composer applies.
 */
internal fun studioCreateRequest(
    text: String,
    language: String,
    postType: String,
    mediaIds: List<String>,
    details: PostDetails = PostDetails(),
): CreatePostRequest = CreatePostRequest(
    text = text,
    visibility = details.visibility?.takeIf { it in SupportedAudience } ?: VISIBILITY_PUBLIC,
    postType = postType,
    mediaIds = mediaIds,
    language = language,
    distribution = DistributionRequest(),
    title = details.title?.trim()?.takeIf { it.isNotEmpty() },
    noComments = details.allowComments?.let { !it },
    commentAccess = details.allowComments?.let { if (it) COMMENT_ACCESS_EVERYONE else COMMENT_ACCESS_NOBODY },
    remixSetting = details.allowReuse?.let { if (it) REMIX_ALLOW else REMIX_DISALLOW },
    coverMediaId = details.coverIndex?.takeIf { postType == POST_TYPE_IMAGE }?.let(mediaIds::getOrNull),
    locationName = details.locationName?.trim()?.takeIf { it.isNotEmpty() },
    locationPlaceId = details.locationPlaceId?.trim()?.takeIf { it.isNotEmpty() && details.locationName != null },
    hashtags = details.hashtags?.takeIf { it.isNotEmpty() },
    mentions = details.mentions?.takeIf { it.isNotEmpty() },
    paidPromotion = details.paidPromotion,
    isBranded = details.isBranded,
    aiGenerated = details.aiGenerated,
    linkUrl = details.linkUrl?.let(PostLink::normalize),
    // The Action button and the members-only level (2026-10-23), each only when set.
    action = details.actionLabel?.takeIf { it.isNotBlank() }?.let { ActionDraft(it, details.actionUrl.orEmpty()) }.toWriteDto(),
    tierRequiredId = details.tierRequiredId?.trim()?.takeIf { it.isNotEmpty() },
)

/**
 * The "Add link" rule, the server's own (post-service `NormalizeLinkURL`):
 * an absolute https URL with a host, no user info (`https://bank@evil`), no
 * whitespace or control characters, at most [MAX_LENGTH] characters.
 * `javascript:`, `data:`, `http:` and every other scheme are refused here so
 * the author hears it on the screen rather than as a failed publish.
 */
object PostLink {
    const val MAX_LENGTH = 500

    /** The link as it will be sent, or null when [raw] is not one we may send. Blank is no link. */
    fun normalize(raw: String): String? {
        val value = raw.trim()
        if (value.isEmpty() || value.length > MAX_LENGTH) return null
        if (value.any { it.isWhitespace() || it.isISOControl() }) return null
        if (!value.startsWith("https://", ignoreCase = true)) return null
        val uri = runCatching { URI(value) }.getOrNull() ?: return null
        if (!uri.scheme.equals("https", ignoreCase = true) || uri.isOpaque) return null
        if (uri.rawUserInfo != null || uri.host.isNullOrBlank()) return null
        return value
    }

    /** What the field says under a link it will not send; null when it is fine or empty. */
    fun problem(raw: String): String? = when {
        raw.isBlank() -> null
        raw.trim().length > MAX_LENGTH -> "A link can be at most $MAX_LENGTH characters."
        normalize(raw) == null -> "Use a full https:// link."
        else -> null
    }
}
