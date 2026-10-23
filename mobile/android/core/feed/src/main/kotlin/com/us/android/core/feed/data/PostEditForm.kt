package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.action.toPostAction
import com.us.android.core.feed.data.credits.CreditRefusals
import com.us.android.core.feed.data.credits.CreditsForm
import com.us.android.core.feed.data.credits.creditsOf
import com.us.android.core.feed.data.credits.releaseYearOrNull
import com.us.android.core.feed.data.credits.toForm
import com.us.android.core.feed.data.credits.toInput
import com.us.android.core.model.FeedItem
import kotlinx.serialization.json.JsonNull
import java.time.LocalDate

/*
 * The owner's Edit sheet, as data (2026-10-21, the web's `editForm.ts`):
 * the form prefilled from the post detail, the PATCH that carries only what
 * changed, the checks made before Save, and what a landed save does to the
 * row on screen. Pure, so the tests pin the diff without a screen.
 */

/** One choice of an enum field: the wire value and the words the sheet shows. */
data class EditChoice(val wire: String, val label: String)

/** What the Edit sheet offers for each enum field — the web's lists and labels. */
object PostEditChoices {
    /** "scheduled" is not offered: rescheduling is its own route, and the edit leaves it alone. */
    val visibility = listOf(
        EditChoice("public", "Public"),
        EditChoice("unlisted", "Unlisted"),
        EditChoice("private", "Private"),
    )
    val license = listOf(
        EditChoice("standard", "Standard"),
        EditChoice("creative_commons", "Creative Commons"),
    )
    val remix = listOf(
        EditChoice("allow", "Allow video and audio"),
        EditChoice("allow_audio_only", "Allow audio only"),
        EditChoice("disallow", "Don't allow"),
    )
    val commentModeration = listOf(
        EditChoice("none", "No moderation"),
        EditChoice("basic", "Basic"),
        EditChoice("strict", "Strict"),
        EditChoice("hold_all", "Hold all"),
    )
    val commentAccess = listOf(
        EditChoice("everyone", "Everyone"),
        EditChoice("followers", "Subscribers only"),
        EditChoice("nobody", "Nobody"),
    )
    val commentSort = listOf(
        EditChoice("top", "Top"),
        EditChoice("newest", "Newest"),
    )
}

/** Every field the sheet edits, as the sheet holds it: tags and hashtags as typed, comma-separated. */
data class PostEditForm(
    val title: String = "",
    val text: String = "",
    val category: String = "",
    val tags: String = "",
    val hashtags: String = "",
    val language: String = "",
    val visibility: String = "public",
    val allowDownload: Boolean = true,
    val noComments: Boolean = false,
    val madeForKids: Boolean = false,
    val paidPromotion: Boolean = false,
    val alteredContent: Boolean = false,
    val license: String = "standard",
    val allowEmbedding: Boolean = true,
    /** "YYYY-MM-DD", or "" for none. */
    val recordingDate: String = "",
    val recordingLocation: String = "",
    val remixSetting: String = "allow",
    val commentModeration: String = "none",
    val commentAccess: String = "everyone",
    val ageRestricted: Boolean = false,
    val hideLikeCount: Boolean = false,
    val defaultCommentSort: String = "top",
)

/** The form for a post, from its detail; an absent enum reads as the server's default, as the web's does. */
fun PostEditDetailDto.toEditForm(): PostEditForm = PostEditForm(
    title = title,
    text = text,
    category = category.ifBlank { categorySlug },
    tags = tags.joinToString(", "),
    hashtags = hashtags.joinToString(", "),
    language = language,
    visibility = visibility.ifBlank { "public" },
    allowDownload = allowDownload,
    noComments = noComments,
    madeForKids = madeForKids,
    paidPromotion = paidPromotion,
    alteredContent = alteredContent,
    license = pick(license, PostEditChoices.license, "standard"),
    allowEmbedding = allowEmbedding,
    recordingDate = recordingDate?.take(DATE_LENGTH)?.takeIf { DATE.matches(it) }.orEmpty(),
    recordingLocation = recordingLocation,
    remixSetting = pick(remixSetting, PostEditChoices.remix, "allow"),
    commentModeration = pick(commentModeration, PostEditChoices.commentModeration, "none"),
    commentAccess = pick(commentAccess, PostEditChoices.commentAccess, "everyone"),
    ageRestricted = ageRestricted,
    hideLikeCount = hideLikeCount,
    defaultCommentSort = pick(defaultCommentSort, PostEditChoices.commentSort, "top"),
)

private fun pick(value: String, choices: List<EditChoice>, fallback: String): String =
    value.takeIf { v -> choices.any { it.wire == v } } ?: fallback

/**
 * Tags as the web splits them: on commas and new lines, trimmed, a leading
 * `#` dropped, blanks and case-insensitive repeats left out, first spelling kept.
 */
fun splitTags(input: String): List<String> {
    val seen = HashSet<String>()
    return input.split(',', '\n')
        .map { it.trim().removePrefix("#") }
        .filter { it.isNotEmpty() && seen.add(it.lowercase()) }
}

/**
 * The PATCH from [base] (as the sheet opened) to [next] (as it is now): only
 * the fields whose value moved. A title is sent trimmed; a cleared recording
 * date goes as "" (the contract's clear); the visibility is never sent while
 * the post is scheduled — the sheet does not offer "scheduled".
 */
@Suppress("CyclomaticComplexMethod", "LongMethod") // One comparison per field: a table, not logic.
fun postEditPatch(base: PostEditForm, next: PostEditForm): PostEditPatch {
    fun <T> moved(a: T, b: T): T? = if (a == b) null else b
    val scheduled = next.visibility == "scheduled"
    return PostEditPatch(
        title = moved(base.title.trim(), next.title.trim()),
        text = moved(base.text, next.text),
        category = moved(base.category, next.category),
        tags = moved(splitTags(base.tags), splitTags(next.tags)),
        hashtags = moved(splitTags(base.hashtags), splitTags(next.hashtags)),
        language = moved(base.language, next.language),
        visibility = if (scheduled) null else moved(base.visibility, next.visibility),
        allowDownload = moved(base.allowDownload, next.allowDownload),
        noComments = moved(base.noComments, next.noComments),
        madeForKids = moved(base.madeForKids, next.madeForKids),
        paidPromotion = moved(base.paidPromotion, next.paidPromotion),
        alteredContent = moved(base.alteredContent, next.alteredContent),
        license = moved(base.license, next.license),
        allowEmbedding = moved(base.allowEmbedding, next.allowEmbedding),
        recordingDate = moved(base.recordingDate.trim(), next.recordingDate.trim()),
        recordingLocation = moved(base.recordingLocation.trim(), next.recordingLocation.trim()),
        remixSetting = moved(base.remixSetting, next.remixSetting),
        commentModeration = moved(base.commentModeration, next.commentModeration),
        commentAccess = moved(base.commentAccess, next.commentAccess),
        ageRestricted = moved(base.ageRestricted, next.ageRestricted),
        hideLikeCount = moved(base.hideLikeCount, next.hideLikeCount),
        defaultCommentSort = moved(base.defaultCommentSort, next.defaultCommentSort),
    )
}

/** The checks made before Save — the web's `detailsProblems`; the server's 422 codes are the backstop. */
fun postEditProblems(form: PostEditForm, today: LocalDate = LocalDate.now()): List<String> = buildList {
    if (form.recordingLocation.trim().codePointCount() > RECORDING_LOCATION_MAX) {
        add("The recording location is too long ($RECORDING_LOCATION_MAX characters at most).")
    }
    val date = form.recordingDate.trim()
    if (date.isNotEmpty()) {
        val parsed = date.takeIf { DATE.matches(it) }?.let { runCatching { LocalDate.parse(it) }.getOrNull() }
        when {
            parsed == null -> add("Write the recording date as YYYY-MM-DD.")
            parsed.isAfter(today) -> add("The recording date can't be in the future.")
        }
    }
}

private fun String.codePointCount(): Int = codePointCount(0, length)

/** What the sheet says when the server refuses the save — the web's sentences, by code. */
fun postEditRefusal(error: AppError): String = when (error) {
    is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Try again when you're connected."
    is AppError.NotFound -> "This video no longer exists."
    else -> POST_EDIT_REFUSALS[error.serverCode()] ?: "Couldn't save your changes. Try again."
}

/** post-service's 4xx codes for the owner edit (`PostEditErrorStatus`) → the web's sentences. */
private val POST_EDIT_REFUSALS: Map<String, String> = mapOf(
    "FORBIDDEN" to "Only the creator can change this video.",
    "TITLE_TOO_LONG" to "The title is too long.",
    "TITLE_REQUIRED" to "A video needs a title.",
    "TEXT_TOO_LONG" to "The description is too long.",
    "EMPTY_POST" to "Add a title or a description.",
    "INVALID_HASHTAG" to "One of the hashtags can't be used.",
    "TOO_MANY_HASHTAGS" to "That is too many hashtags.",
    "INVALID_TAGS" to "Too many tags, or one is too long.",
    "INVALID_CATEGORY" to "Pick a category from the list.",
    "INVALID_VISIBILITY" to "That visibility is not one we offer.",
    "INVALID_LANGUAGE" to "Pick a language from the list.",
    "INVALID_LICENSE" to "That license is not one we offer.",
    "INVALID_RECORDING_DATE" to "The recording date can't be in the future.",
    "INVALID_RECORDING_LOCATION" to "The recording location is too long ($RECORDING_LOCATION_MAX characters at most).",
    "INVALID_REMIX_SETTING" to "That remix setting is not one we offer.",
    "INVALID_COMMENT_MODERATION" to "That comment moderation level is not one we offer.",
    "INVALID_COMMENT_ACCESS" to "That choice of who can comment is not one we offer.",
    "INVALID_COMMENT_SORT" to "Comments can open on Top or Newest only.",
) + CreditRefusals // Video credits (2026-10-23): the Credits section's own sentences.

/**
 * The credits part of the PATCH, from [base] (as the sheet opened) to
 * [next]: the whole list when anything in it moved — `credits` REPLACES the
 * list, so a reorder, a rename or a removal all send it entire, and `[]`
 * clears it, as `genres: []` clears the genres. A year or production house
 * the owner cleared goes as an explicit JSON `null` (the contract's clear);
 * unchanged fields stay off the wire. Every row with a tag resends its
 * `user_id`, so a pending, accepted or declined tag keeps its status.
 */
fun creditsPatch(base: CreditsForm, next: CreditsForm): PostEditPatch {
    val nextYear = next.releaseYearOrNull()
    val nextHouse = next.productionHouse.trim()
    return PostEditPatch(
        credits = next.toInput().takeIf { it != base.toInput() },
        genres = next.genres.takeIf { it != base.genres },
        releaseYear = patchYear(nextYear).takeIf { nextYear != base.releaseYearOrNull() },
        productionHouse = patchText(nextHouse).takeIf { nextHouse != base.productionHouse.trim() },
    )
}

/** The owner's credits of a post detail, as the editor opens them. */
fun PostEditDetailDto.creditsForm(keyFor: (Int) -> String): CreditsForm =
    creditsOf(credits, genres, releaseYear, productionHouse).toForm(keyFor)

/**
 * A landed save, applied to the row on screen: the fields a row carries take
 * the patch's values, everything else stays. The server's answer would say
 * the same; applying what was sent keeps every list right without a refetch.
 */
fun FeedItem.withEdits(patch: PostEditPatch): FeedItem = copy(
    title = patch.title ?: title,
    text = patch.text ?: text,
    hashtags = patch.hashtags ?: hashtags,
    visibility = patch.visibility ?: visibility,
    controls = controls.copy(
        noComments = patch.noComments ?: controls.noComments,
        allowDownload = patch.allowDownload ?: controls.allowDownload,
        category = patch.category ?: controls.category,
        remixSetting = patch.remixSetting ?: controls.remixSetting,
        commentAccess = patch.commentAccess ?: controls.commentAccess,
    ),
    disclosure = disclosure.copy(paidPromotion = patch.paidPromotion ?: disclosure.paidPromotion),
    // The Action button: the saved answer's button (PostEditRepository.save), JSON null when removed.
    action = when (val saved = patch.action) {
        null -> action
        is JsonNull -> null
        else -> saved.toPostAction() ?: action
    },
)

/** The web's cap on the recording location, in characters. */
const val RECORDING_LOCATION_MAX = 100

private const val DATE_LENGTH = 10
private val DATE = Regex("""\d{4}-\d{2}-\d{2}""")
