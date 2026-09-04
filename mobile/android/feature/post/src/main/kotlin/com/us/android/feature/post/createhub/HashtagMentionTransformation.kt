package com.us.android.feature.post.createhub

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.input.OffsetMapping
import androidx.compose.ui.text.input.TransformedText
import androidx.compose.ui.text.input.VisualTransformation

/**
 * Paints `#hashtags` and `@mentions` in the accent colour as the user types.
 *
 * Display only: the text on the wire is untouched, and the server extracts
 * hashtags from it — so the client's idea of "what is a hashtag" is purely
 * cosmetic and can never disagree with the record. Identity offset mapping
 * because no characters are added or removed.
 */
class HashtagMentionTransformation(private val accent: Color) : VisualTransformation {

    override fun filter(text: AnnotatedString): TransformedText {
        val styled = buildAnnotatedString {
            append(text.text)
            TOKEN.findAll(text.text).forEach { match ->
                addStyle(SpanStyle(color = accent), match.range.first, match.range.last + 1)
            }
        }
        return TransformedText(styled, OffsetMapping.Identity)
    }

    companion object {
        /**
         * A `#` or `@` that starts a word (not `a#b`), followed by letters,
         * digits or underscores in any script.
         */
        val TOKEN: Regex = Regex("(?<![\\p{L}\\p{N}_])[#@][\\p{L}\\p{N}_]+")
    }
}
