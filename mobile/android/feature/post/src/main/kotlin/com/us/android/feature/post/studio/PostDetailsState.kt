package com.us.android.feature.post.studio

import com.us.android.core.creator.model.PostDetails
import com.us.android.feature.post.createhub.HashtagMentionTransformation
import com.us.android.feature.post.createhub.Hashtags
import com.us.android.feature.post.data.dto.VISIBILITY_FRIENDS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import kotlinx.serialization.Serializable

/**
 * What the post details screen edits (post-screens parity, 2026-10-18),
 * beside the caption the project document already holds. One value for the
 * Studio's details step and the draft editor, so both screens are the same
 * form. Saveable as JSON, so a process death mid-form loses nothing.
 */
@Serializable
data class PostDetailsUi(
    val title: String = "",
    val visibility: String = VISIBILITY_PUBLIC,
    val allowComments: Boolean = true,
    val allowReuse: Boolean = true,
    val paidPromotion: Boolean = false,
    val isBranded: Boolean = false,
    val aiGenerated: Boolean = false,
    /** What is typed in the link field; validated by [PostLink]. */
    val link: String = "",
    val locationName: String = "",
    val locationPlaceId: String? = null,
    /** Which photo is the cover, 0-based. */
    val coverIndex: Int = 0,
) {
    /** Why the link will not be sent, or null. */
    val linkProblem: String? get() = PostLink.problem(link)

    /** Post may go: nothing typed is wrong. */
    val valid: Boolean get() = linkProblem == null && title.length <= MAX_TITLE

    /**
     * The details as they are published. Every switch is sent explicitly —
     * the screen shows each one, so each is a decision — and the tags and
     * mentions are what the description says (#tag, @name), capped as the
     * server caps them. The cover is sent for a carousel only: one photo is
     * its own cover.
     */
    fun toDetails(description: String, pageCount: Int): PostDetails {
        val (hashtags, mentions) = descriptionTags(description)
        return PostDetails(
            title = title.trim().takeIf { it.isNotEmpty() },
            hashtags = hashtags.takeIf { it.isNotEmpty() },
            mentions = mentions.takeIf { it.isNotEmpty() },
            visibility = visibility,
            allowComments = allowComments,
            allowReuse = allowReuse,
            paidPromotion = paidPromotion,
            isBranded = isBranded,
            aiGenerated = aiGenerated,
            linkUrl = PostLink.normalize(link),
            locationName = locationName.trim().takeIf { it.isNotEmpty() },
            locationPlaceId = locationPlaceId?.takeIf { locationName.isNotBlank() },
            coverIndex = coverIndex.takeIf { pageCount > 1 && it in 0 until pageCount },
        )
    }

    /** The cover after the pages changed: kept on its photo when it can be, else the first. */
    fun withPageCount(pageCount: Int): PostDetailsUi =
        if (coverIndex in 0 until pageCount.coerceAtLeast(1)) this else copy(coverIndex = 0)

    companion object {
        const val MAX_TITLE = 100

        /** A saved draft's details, as the form edits them. */
        fun from(details: PostDetails): PostDetailsUi = PostDetailsUi(
            title = details.title.orEmpty(),
            visibility = details.visibility?.takeIf { it in AUDIENCES } ?: VISIBILITY_PUBLIC,
            allowComments = details.allowComments ?: true,
            allowReuse = details.allowReuse ?: true,
            paidPromotion = details.paidPromotion ?: false,
            isBranded = details.isBranded ?: false,
            aiGenerated = details.aiGenerated ?: false,
            link = details.linkUrl.orEmpty(),
            locationName = details.locationName.orEmpty(),
            locationPlaceId = details.locationPlaceId,
            coverIndex = details.coverIndex ?: 0,
        )
    }
}

/** The three audiences the screen offers, in its order: Everyone, Friends, Only you. */
val AUDIENCES: List<String> = listOf(VISIBILITY_PUBLIC, VISIBILITY_FRIENDS, VISIBILITY_PRIVATE)

/** What the audience row says for [visibility]. */
fun audienceLabel(visibility: String): String = when (visibility) {
    VISIBILITY_FRIENDS -> "Friends can view this post"
    VISIBILITY_PRIVATE -> "Only you can view this post"
    else -> "Everyone can view this post"
}

/**
 * The `#tags` and `@names` a description carries, in order, each once
 * (case-insensitively), capped at the server's thirty and twenty — the
 * same tokens [HashtagMentionTransformation] paints. Pure.
 */
fun descriptionTags(text: String): Pair<List<String>, List<String>> {
    val tags = mutableListOf<String>()
    val names = mutableListOf<String>()
    HashtagMentionTransformation.TOKEN.findAll(text).forEach { match ->
        val token = match.value
        val body = token.drop(1)
        if (token.startsWith("#")) {
            if (tags.size < Hashtags.MAX_HASHTAGS && tags.none { it.equals(body, ignoreCase = true) }) tags += body
        } else if (names.size < Hashtags.MAX_MENTIONS && names.none { it.equals(body, ignoreCase = true) }) {
            names += body
        }
    }
    return tags to names
}

/**
 * The description after the "#" chip: a `#` at the end, on a word boundary,
 * so the next letters typed are a tag. Pure.
 */
fun insertHashMark(text: String): String = when {
    text.isEmpty() -> "#"
    text.last().isWhitespace() -> "$text#"
    else -> "$text #"
}

/**
 * The description after a mention was picked: `@username` and a space at
 * the end, replacing a bare `@` the author had already typed there. Pure.
 */
fun insertMention(text: String, username: String): String {
    val name = Hashtags.username(username)
    if (name.isEmpty()) return text
    val base = text.removeSuffix("@")
    val joined = when {
        base.isEmpty() -> ""
        base.last().isWhitespace() -> base
        else -> "$base "
    }
    return "$joined@$name "
}
