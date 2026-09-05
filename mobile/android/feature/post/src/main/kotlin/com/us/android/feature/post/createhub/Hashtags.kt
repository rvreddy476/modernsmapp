package com.us.android.feature.post.createhub

/**
 * The hashtag chips' rules (2026-09-05: a HASHTAGS field of its own, no
 * tags mixed into the caption). Pure, so every rule is a table test.
 *
 *  - Typing `#a b`, `a, b` or `a,b` splits into chips: whitespace and
 *    commas both separate, and the `#` the user may type is stripped.
 *  - A tag is letters, digits and underscores; anything else typed is
 *    dropped from the chip rather than sent for the server to refuse.
 *  - Chips are unique, case-insensitively, keeping the first spelling.
 *  - At most [MAX_HASHTAGS] chips; the rest of a paste is dropped.
 */
object Hashtags {
    /** The server's cap — thirty. */
    const val MAX_HASHTAGS = 30

    /** The server's cap on mentioned people — twenty. */
    const val MAX_MENTIONS = 20

    private val separators = Regex("[\\s,#]+")
    private val allowed = Regex("[^\\p{L}\\p{N}_]")

    /** The chips [existing] becomes once [typed] is added to it. */
    fun add(existing: List<String>, typed: String): List<String> {
        val result = existing.toMutableList()
        val seen = existing.mapTo(mutableSetOf()) { it.lowercase() }
        typed.split(separators)
            .map { it.replace(allowed, "") }
            .filter { it.isNotBlank() }
            .forEach { tag ->
                if (result.size >= MAX_HASHTAGS) return@forEach
                if (seen.add(tag.lowercase())) result += tag
            }
        return result
    }

    /**
     * Whether the text in the field should be committed to chips now:
     * a separator was typed after something — `#tag ` or `tag,`.
     */
    fun shouldCommit(typed: String): Boolean {
        val last = typed.lastOrNull() ?: return false
        return (last.isWhitespace() || last == ',') && typed.trim(' ', ',', '#').isNotEmpty()
    }

    /** Strips a leading `@` and surrounding space: what a mention chip carries. */
    fun username(raw: String): String = raw.trim().removePrefix("@").trim()
}

/** What the hashtag field does: type, commit, remove a chip, take a suggestion. */
internal class HashtagActions(
    val onInputChanged: (String) -> Unit,
    val onCommit: () -> Unit,
    val onRemove: (String) -> Unit,
    val onPickSuggestion: (String) -> Unit,
)
