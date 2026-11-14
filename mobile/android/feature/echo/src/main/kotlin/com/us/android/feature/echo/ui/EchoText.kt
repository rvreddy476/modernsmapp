@file:Suppress("MatchingDeclarationName") // the tokens and the text built from them

package com.us.android.feature.echo.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withLink
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.LocalUsRichTextLinks
import com.us.android.core.ui.RichToken
import com.us.android.core.ui.UsRichTextLinks
import com.us.android.core.ui.richTokens

/** One link in a note's text: a mention, a hashtag or a web address. */
sealed interface EchoToken {
    val start: Int
    val end: Int

    data class Mention(override val start: Int, override val end: Int, val username: String) : EchoToken
    data class Hashtag(override val start: Int, override val end: Int, val tag: String) : EchoToken
    data class Url(override val start: Int, override val end: Int, val url: String) : EchoToken
}

/**
 * The links in a note, in order, never overlapping: the app's own mention and
 * hashtag rules (`richTokens`), plus `http(s)://` addresses. An address wins
 * over a `#` or `@` inside it; trailing punctuation is left out of it.
 */
fun echoTokens(text: String): List<EchoToken> {
    val urls = URL.findAll(text).map { match ->
        val url = match.value.trimEnd { it in TRAILING }
        EchoToken.Url(match.range.first, match.range.first + url.length, url)
    }.filter { it.url.length > MIN_URL }.toList()
    val rich = richTokens(text).filter { token -> urls.none { token.start < it.end && it.start < token.end } }.map {
        when (it) {
            is RichToken.Mention -> EchoToken.Mention(it.start, it.end, it.username)
            is RichToken.Hashtag -> EchoToken.Hashtag(it.start, it.end, it.tag)
        }
    }
    return (urls + rich).sortedBy { it.start }
}

/**
 * [text] with X's tappable runs in the accent colour: mentions through the
 * host's [links], hashtags to Echo's own hashtag page, addresses to the
 * browser. Plain when there is nothing to link.
 */
fun echoRichText(text: String, links: UsRichTextLinks?, onHashtag: (String) -> Unit, color: Color): AnnotatedString {
    val tokens = echoTokens(text)
    if (tokens.isEmpty()) return AnnotatedString(text)
    val style = TextLinkStyles(style = SpanStyle(color = color))
    return buildAnnotatedString {
        var at = 0
        for (token in tokens) {
            append(text, at, token.start)
            val link = when (token) {
                is EchoToken.Url -> LinkAnnotation.Url(token.url, style)
                is EchoToken.Hashtag -> LinkAnnotation.Clickable("hashtag:${token.tag}", style) { onHashtag(token.tag) }
                is EchoToken.Mention -> LinkAnnotation.Clickable("mention:${token.username}", style) {
                    links?.onMention?.invoke(token.username)
                }
            }
            withLink(link) { append(text, token.start, token.end) }
            at = token.end
        }
        append(text, at, text.length)
    }
}

/** [echoRichText] with the host's mention links and Echo's hashtag page. */
@Composable
fun rememberEchoText(text: String): AnnotatedString {
    val links = LocalUsRichTextLinks.current
    val hashtag = LocalEchoHashtag.current
    // Echo's own ember (echo-look-v3), readable in both themes.
    val color = UsTheme.extended.echo.text
    return remember(text, links, hashtag, color) { echoRichText(text, links, hashtag, color) }
}

/** Where a tapped hashtag goes inside Echo: its Echo page, not VChat's. */
val LocalEchoHashtag = androidx.compose.runtime.staticCompositionLocalOf<(String) -> Unit> { {} }

/** Wraps an Echo screen so hashtags in its text open Echo's hashtag page. */
@Composable
fun EchoLinks(destinations: EchoDestinations, content: @Composable () -> Unit) {
    CompositionLocalProvider(LocalEchoHashtag provides destinations.onOpenHashtag, content = content)
}

private val URL = Regex("""https?://[^\s<>"]+""")
private val TRAILING = charArrayOf('.', ',', ';', ':', '!', '?', ')', '\'', '"')
private const val MIN_URL = 8
