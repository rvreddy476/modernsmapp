package com.us.android.core.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withLink

/*
 * Tappable @mentions and #hashtags (2026-10-24, the web's PostCard and
 * comments/mentions.ts): in captions, comments and bios. A mention opens the
 * person's profile (`/u/<username>` on the web; here the handle is resolved
 * to an id by the host), a hashtag its posts (`/hashtag/<tag>`).
 *
 * The host — `:app` — provides [LocalUsRichTextLinks] once, over the whole
 * graph, so every surface that draws text links the same way without each
 * screen threading two more callbacks through its rows. Without a provider
 * the text renders plain, exactly as before.
 */

/** One linkable run of [text][richTokens]: its range, and what it names (without the `@` / `#`). */
sealed interface RichToken {
    val start: Int
    val end: Int

    data class Mention(override val start: Int, override val end: Int, val username: String) : RichToken
    data class Hashtag(override val start: Int, override val end: Int, val tag: String) : RichToken
}

/**
 * The mentions and hashtags in [text], in order, never overlapping.
 *
 * A mention is the web's rule (mentions.ts `MENTION`): `@` not after a
 * letter, digit, `_` or `@` (so an e-mail address is not one), then 2–30 of
 * `[A-Za-z0-9._]`, a trailing dot being punctuation ("@bob." links bob). A
 * hashtag is `#` then letters, marks, digits or `_` (PostCard's
 * `#[\p{L}\p{M}\p{N}_]+`), not after a word character or `&` (an HTML
 * entity, `&#39;`), and not all digits ("#1" is a number).
 */
fun richTokens(text: String): List<RichToken> {
    if (text.isEmpty()) return emptyList()
    val tokens = mutableListOf<RichToken>()
    MENTION.findAll(text).forEach { match ->
        val group = match.groups[1] ?: return@forEach
        val username = group.value.trimEnd('.')
        if (username.length >= MIN_HANDLE) {
            val start = group.range.first - 1
            tokens += RichToken.Mention(start, start + 1 + username.length, username)
        }
    }
    HASHTAG.findAll(text).forEach { match ->
        val group = match.groups[1] ?: return@forEach
        if (group.value.any { !it.isDigit() }) {
            val start = group.range.first - 1
            tokens += RichToken.Hashtag(start, group.range.last + 1, group.value)
        }
    }
    return tokens.sortedBy { it.start }.fold(mutableListOf()) { kept, token ->
        if (kept.isEmpty() || token.start >= kept.last().end) kept += token
        kept
    }
}

private val MENTION = Regex("""(?<![A-Za-z0-9_@])@([A-Za-z0-9._]{2,30})""")
private val HASHTAG = Regex("""(?<![\p{L}\p{M}\p{N}_&#])#([\p{L}\p{M}\p{N}_]+)""")
private const val MIN_HANDLE = 2

/** Where a tapped mention or hashtag goes; provided once by the host. */
@Immutable
class UsRichTextLinks(
    val onMention: (username: String) -> Unit,
    val onHashtag: (tag: String) -> Unit,
)

/** Null (the default) draws mentions and hashtags as plain text. */
val LocalUsRichTextLinks = staticCompositionLocalOf<UsRichTextLinks?> { null }

/**
 * [text] with each mention and hashtag a link, styled [linkStyle] (bold by
 * default, in the text's own colour — the web's `font-medium`, no new colour).
 * Plain when no [links] are provided.
 */
fun richText(
    text: String,
    links: UsRichTextLinks?,
    linkStyle: SpanStyle = SpanStyle(fontWeight = FontWeight.SemiBold),
): AnnotatedString {
    if (links == null) return AnnotatedString(text)
    val tokens = richTokens(text)
    if (tokens.isEmpty()) return AnnotatedString(text)
    return buildAnnotatedString {
        var at = 0
        for (token in tokens) {
            append(text, at, token.start)
            val link = when (token) {
                is RichToken.Mention -> LinkAnnotation.Clickable(
                    tag = "mention:${token.username}",
                    styles = TextLinkStyles(style = linkStyle),
                ) { links.onMention(token.username) }
                is RichToken.Hashtag -> LinkAnnotation.Clickable(
                    tag = "hashtag:${token.tag}",
                    styles = TextLinkStyles(style = linkStyle),
                ) { links.onHashtag(token.tag) }
            }
            withLink(link) { append(text, token.start, token.end) }
            at = token.end
        }
        append(text, at, text.length)
    }
}

/** [richText] with the host's links. */
@Composable
fun rememberRichText(text: String, linkStyle: SpanStyle = SpanStyle(fontWeight = FontWeight.SemiBold)): AnnotatedString {
    val links = LocalUsRichTextLinks.current
    return remember(text, links, linkStyle) { richText(text, links, linkStyle) }
}
