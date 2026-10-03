package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.MAX_COMMENT_LENGTH
import com.us.android.core.engagement.data.isValidComment
import org.junit.Test

/**
 * Protects the comment box's validation for emoji (2026-10-02): the shared
 * comments sheet under reels, long videos and posts. An emoji is one
 * character toward the cap, emoji alone is a comment, and blank is not.
 */
class CommentValidationTest {

    @Test
    fun `emoji alone is a valid comment`() {
        assertThat("🔥".isValidComment()).isTrue()
        assertThat("  ❤️ ".isValidComment()).isTrue()
        assertThat("great video 🎉🎉".isValidComment()).isTrue()
    }

    @Test
    fun `the cap counts code points, so a full cap of emoji is still a comment`() {
        val emoji = "😀".repeat(MAX_COMMENT_LENGTH)

        // Twice the cap in UTF-16 units: `length` would have refused it.
        assertThat(emoji.length).isEqualTo(2 * MAX_COMMENT_LENGTH)
        assertThat(emoji.isValidComment()).isTrue()
        assertThat((emoji + "😀").isValidComment()).isFalse()
    }

    @Test
    fun `plain text keeps its cap, and blank is refused`() {
        assertThat("x".repeat(MAX_COMMENT_LENGTH).isValidComment()).isTrue()
        assertThat("x".repeat(MAX_COMMENT_LENGTH + 1).isValidComment()).isFalse()
        assertThat("".isValidComment()).isFalse()
        assertThat("   \n".isValidComment()).isFalse()
    }
}
