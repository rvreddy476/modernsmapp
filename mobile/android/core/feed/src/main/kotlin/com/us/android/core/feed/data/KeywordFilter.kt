package com.us.android.core.feed.data

import com.us.android.core.model.FeedItem

/**
 * Client-side fallback for muted keywords.
 *
 * The server filters ranked feeds by the same list; this hides a post the
 * ranking service has not caught up on yet, and covers a page that was
 * already cached when the keyword was added. Matching is whole-word,
 * case-insensitive, and hashtag-agnostic (`#cats` matches `cats`), which is
 * the same normalisation the server applies when it stores the list.
 */
object KeywordFilter {

    fun matches(text: String, keywords: List<String>): Boolean {
        if (keywords.isEmpty() || text.isBlank()) return false
        val words = text.lowercase().split(WORD_BOUNDARY).filter { it.isNotEmpty() }.toHashSet()
        return keywords.any { keyword ->
            val normalised = keyword.trim().lowercase().removePrefix("#")
            normalised.isNotEmpty() && (
                if (normalised.contains(' ')) {
                    text.lowercase().contains(normalised)
                } else {
                    normalised in words
                }
                )
        }
    }

    fun hides(item: FeedItem, keywords: List<String>): Boolean = matches(item.text, keywords)

    private val WORD_BOUNDARY = Regex("[^\\p{L}\\p{N}_]+")
}
