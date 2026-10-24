package com.us.android.core.ui

import androidx.compose.ui.text.LinkAnnotation
import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Mentions and hashtags in captions, comments and bios (2026-10-24), by the
 * web's rules: comments/mentions.ts `MENTION` and PostCard's hashtag split.
 */
class UsRichTextTest {

    private fun spans(text: String) = richTokens(text).map { text.substring(it.start, it.end) }

    @Test
    fun `a mention and a hashtag are found with their ranges`() {
        val text = "hi @ana_b see #sunset"
        val tokens = richTokens(text)

        assertThat(tokens).containsExactly(
            RichToken.Mention(3, 9, "ana_b"),
            RichToken.Hashtag(14, 21, "sunset"),
        ).inOrder()
    }

    @Test
    fun `a trailing dot is punctuation, an inner dot is part of the name`() {
        assertThat(spans("thanks @bob.")).containsExactly("@bob")
        assertThat(spans("cc @ana.lee!")).containsExactly("@ana.lee")
    }

    @Test
    fun `an e-mail address and a one-letter handle are not mentions`() {
        assertThat(spans("mail me at ana@example.com")).isEmpty()
        assertThat(spans("@a is short")).isEmpty()
        assertThat(spans("@@ana")).isEmpty()
    }

    @Test
    fun `a handle longer than 30 characters links only its first 30`() {
        val long = "a".repeat(35)
        assertThat(richTokens("@$long").single()).isEqualTo(RichToken.Mention(0, 31, "a".repeat(30)))
    }

    @Test
    fun `hashtags take letters in any script, digits and underscores but not all digits`() {
        assertThat(spans("#café_2026 #日本 #1 #42abc")).containsExactly("#café_2026", "#日本", "#42abc").inOrder()
    }

    @Test
    fun `a hashtag inside a word or an HTML entity is not one`() {
        assertThat(spans("abc#def")).isEmpty()
        assertThat(spans("it&#39;s")).isEmpty()
        assertThat(spans("##double")).isEmpty()
    }

    @Test
    fun `a line start and punctuation before the sign are fine`() {
        assertThat(spans("#start (@ana) and,#end")).containsExactly("#start", "@ana", "#end").inOrder()
    }

    @Test
    fun `without links the text is plain, with them each token is a link`() {
        val text = "hi @ana see #sun"
        assertThat(richText(text, links = null).getLinkAnnotations(0, text.length)).isEmpty()

        val opened = mutableListOf<String>()
        val rich = richText(text, UsRichTextLinks(onMention = { opened += "u:$it" }, onHashtag = { opened += "t:$it" }))
        val links = rich.getLinkAnnotations(0, text.length)

        assertThat(rich.text).isEqualTo(text)
        assertThat(links.map { text.substring(it.start, it.end) }).containsExactly("@ana", "#sun").inOrder()
        links.forEach { (it.item as LinkAnnotation.Clickable).linkInteractionListener?.onClick(it.item) }
        assertThat(opened).containsExactly("u:ana", "t:sun").inOrder()
    }

    @Test
    fun `text with no tokens comes back as itself`() {
        assertThat(richTokens("")).isEmpty()
        assertThat(richText("plain words", UsRichTextLinks({}, {})).text).isEqualTo("plain words")
    }
}
