package com.us.android.feature.post.studio

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class PostLanguagesTest {

    @Test
    fun `a code shows as its language name, never the raw code`() {
        assertThat(postLanguageLabel("en")).isEqualTo("English")
        assertThat(postLanguageLabel("te")).isEqualTo("Telugu")
    }

    @Test
    fun `a region-tagged code shows and selects its base language`() {
        assertThat(postLanguageLabel("en-IN")).isEqualTo("English")
        assertThat(postLanguageSelection("en-IN")).isEqualTo("en")
    }

    @Test
    fun `a code outside the list falls back to the platform name`() {
        assertThat(postLanguageLabel("nl")).isEqualTo("Dutch")
    }

    @Test
    fun `an empty code says it is not set`() {
        assertThat(postLanguageLabel("  ")).isEqualTo("Not set")
    }

    @Test
    fun `every option value is a bare language code and unique`() {
        val values = POST_LANGUAGE_OPTIONS.map { it.value }
        assertThat(values).containsNoDuplicates()
        values.forEach { assertThat(it).matches("[a-z]{2,3}") }
    }
}
