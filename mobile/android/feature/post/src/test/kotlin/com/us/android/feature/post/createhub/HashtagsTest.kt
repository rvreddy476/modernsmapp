package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The HASHTAGS field's rules as a table: split, strip `#`, dedupe, cap at thirty. */
class HashtagsTest {

    @Test
    fun `spaces, commas and hashes all split`() {
        assertThat(Hashtags.add(emptyList(), "#a b")).containsExactly("a", "b").inOrder()
        assertThat(Hashtags.add(emptyList(), "a, b,c")).containsExactly("a", "b", "c").inOrder()
        assertThat(Hashtags.add(emptyList(), "#one#two")).containsExactly("one", "two").inOrder()
        assertThat(Hashtags.add(emptyList(), "  #spaced   out  ")).containsExactly("spaced", "out").inOrder()
    }

    @Test
    fun `the hash is stripped and only word characters survive`() {
        assertThat(Hashtags.add(emptyList(), "#Sunday-skate!")).containsExactly("Sundayskate")
        assertThat(Hashtags.add(emptyList(), "snake_case")).containsExactly("snake_case")
        assertThat(Hashtags.add(emptyList(), "தமிழ்")).hasSize(1)
        assertThat(Hashtags.add(emptyList(), "#!!!")).isEmpty()
        assertThat(Hashtags.add(emptyList(), "")).isEmpty()
    }

    @Test
    fun `duplicates are dropped case-insensitively, keeping the first spelling`() {
        val chips = Hashtags.add(listOf("Longboard"), "longboard LONGBOARD skate Skate")

        assertThat(chips).containsExactly("Longboard", "skate").inOrder()
    }

    @Test
    fun `thirty is the cap and the rest of a paste is dropped`() {
        val many = (1..40).joinToString(" ") { "tag$it" }

        val chips = Hashtags.add(emptyList(), many)

        assertThat(chips).hasSize(Hashtags.MAX_HASHTAGS)
        assertThat(chips.first()).isEqualTo("tag1")
        assertThat(chips.last()).isEqualTo("tag30")
        assertThat(Hashtags.add(chips, "tag31")).hasSize(Hashtags.MAX_HASHTAGS)
    }

    @Test
    fun `a separator after a tag commits, and a separator alone does not`() {
        assertThat(Hashtags.shouldCommit("#tag ")).isTrue()
        assertThat(Hashtags.shouldCommit("tag,")).isTrue()
        assertThat(Hashtags.shouldCommit("tag")).isFalse()
        assertThat(Hashtags.shouldCommit(" ")).isFalse()
        assertThat(Hashtags.shouldCommit("#,")).isFalse()
        assertThat(Hashtags.shouldCommit("")).isFalse()
    }

    @Test
    fun `a mention chip carries the bare username`() {
        assertThat(Hashtags.username(" @maya ")).isEqualTo("maya")
        assertThat(Hashtags.username("maya")).isEqualTo("maya")
        assertThat(Hashtags.MAX_MENTIONS).isEqualTo(MAX_TAGGED_PEOPLE)
    }
}
