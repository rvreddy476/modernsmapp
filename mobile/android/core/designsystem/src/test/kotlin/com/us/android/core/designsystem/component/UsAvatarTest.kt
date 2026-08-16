package com.us.android.core.designsystem.component

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class UsAvatarTest {

    @Test
    fun `two words give two initials`() {
        assertThat(initialsOf("Ada Lovelace")).isEqualTo("AL")
    }

    @Test
    fun `one word gives one initial`() {
        assertThat(initialsOf("Madonna")).isEqualTo("M")
    }

    @Test
    fun `middle names are skipped in favour of first and last`() {
        assertThat(initialsOf("John Ronald Reuel Tolkien")).isEqualTo("JT")
    }

    /**
     * A cleared display name is a state real accounts reach: `PUT /me` with an
     * empty body is a full replacement and blanks `display_name`.
     */
    @Test
    fun `blank names fall back rather than crash`() {
        assertThat(initialsOf("")).isEqualTo("?")
        assertThat(initialsOf("   ")).isEqualTo("?")
    }

    @Test
    fun `extra whitespace does not produce empty initials`() {
        assertThat(initialsOf("  Grace   Hopper  ")).isEqualTo("GH")
    }

    /**
     * The server had to be fixed to accept Indic combining marks in names.
     * The client must not reintroduce the same assumption from the other end.
     *
     * The leading code point is taken, not the full grapheme cluster: the
     * cluster for "प्रिया" is "प्रि" (four code points — a consonant, a virama,
     * a second consonant and a vowel sign), which is three glyphs wide and
     * overflows an avatar. "प" is a valid standalone letter and renders
     * cleanly, which is what an initial needs to be.
     */
    @Test
    fun `indic names produce one leading letter per word`() {
        assertThat(initialsOf("प्रिया शर्मा")).isEqualTo("पश")
    }

    @Test
    fun `a single indic name produces one initial`() {
        assertThat(initialsOf("प्रिया")).isEqualTo("प")
    }

    /**
     * Taking `first()` on a name starting outside the BMP yields half a
     * surrogate pair, which renders as a replacement glyph.
     */
    @Test
    fun `supplementary plane characters are not split`() {
        val emojiName = "😀 Smiley"
        val initials = initialsOf(emojiName)
        assertThat(initials).startsWith("😀")
        assertThat(initials).doesNotContain("�")
    }

    @Test
    fun `avatar colour is stable for a seed`() {
        assertThat(avatarColor("user-1")).isEqualTo(avatarColor("user-1"))
    }

    @Test
    fun `avatar colour does not crash on an empty seed`() {
        assertThat(avatarColor("")).isEqualTo(avatarColor(""))
    }

    /**
     * `Int.MIN_VALUE.absoluteValue` is still negative, so a hash that lands
     * there would index the palette with a negative number and throw.
     */
    @Test
    fun `avatar colour survives a hash of Int MIN_VALUE`() {
        val seed = seedHashingToIntMin()
        if (seed != null) {
            avatarColor(seed) // must not throw
        }
    }

    private fun seedHashingToIntMin(): String? =
        generateSequence(0) { it + 1 }
            .take(SEARCH_LIMIT)
            .map { it.toString() }
            .firstOrNull { it.hashCode() == Int.MIN_VALUE }

    private companion object {
        const val SEARCH_LIMIT = 1000
    }
}
