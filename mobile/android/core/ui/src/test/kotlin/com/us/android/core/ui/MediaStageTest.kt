package com.us.android.core.ui

import androidx.compose.material3.Typography
import androidx.compose.ui.layout.ContentScale
import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The full-screen stage's pure rules (2026-10-18). */
class MediaStageTest {

    private val screen = 9f / 19.5f

    @Test
    fun `a picture close to the screen's shape is cropped, anything else is fitted`() {
        assertThat(stageContentScale(9f / 16f, screen)).isEqualTo(ContentScale.Crop)
        assertThat(stageContentScale(3f / 4f, screen)).isEqualTo(ContentScale.Fit)
        assertThat(stageContentScale(16f / 9f, screen)).isEqualTo(ContentScale.Fit)
        assertThat(stageContentScale(1f, screen)).isEqualTo(ContentScale.Fit)
        assertThat(stageContentScale(0f, screen)).isEqualTo(ContentScale.Fit)
    }

    @Test
    fun `the counter is one-based and clamped`() {
        assertThat(stagePageCounter(0, 5)).isEqualTo("1/5")
        assertThat(stagePageCounter(4, 5)).isEqualTo("5/5")
        assertThat(stagePageCounter(9, 5)).isEqualTo("5/5")
        assertThat(stagePageCounter(-1, 3)).isEqualTo("1/3")
        assertThat(stagePageCounter(0, 0)).isEqualTo("0/0")
    }

    @Test
    fun `a rail control says its name, and its label when that adds something`() {
        assertThat(stageControlDescription("Like", "8.8K")).isEqualTo("Like, 8.8K")
        assertThat(stageControlDescription("Share", "Share")).isEqualTo("Share")
        assertThat(stageControlDescription("Mute", null)).isEqualTo("Mute")
    }

    @Test
    fun `a short text post starts large, a long one at body size`() {
        val type = Typography()
        assertThat(stageTextStyle(20, type)).isEqualTo(type.headlineLarge)
        assertThat(stageTextStyle(150, type)).isEqualTo(type.headlineSmall)
        assertThat(stageTextStyle(400, type)).isEqualTo(type.titleLarge)
        assertThat(stageTextStyle(2_000, type)).isEqualTo(type.bodyLarge)
    }

    @Test
    fun `a tap on the current tab is an event, never replayed to a screen composed later`() {
        val chrome = ChromeVisibility()
        chrome.onTabReselected()
        assertThat(chrome.reselected.replayCache).isEmpty()
    }
}
