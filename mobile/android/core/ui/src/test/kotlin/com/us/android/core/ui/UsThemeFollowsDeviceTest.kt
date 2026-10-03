package com.us.android.core.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.junit4.createComposeRule
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The app follows the device's light / dark setting (founder, 2026-10-02).
 *
 * `UsTheme` used to default to dark whatever the device said, so the light
 * palette was never seen. What is pinned here is the default: a screen that
 * just writes `UsTheme { }`, which is every screen but the live ones, gets
 * the light theme on a light device and the dark theme on a dark one, and a
 * screen that asks for dark on purpose still gets it.
 *
 * The grounds are told apart by lightness rather than by value: the values
 * are `UsWebThemeParityTest`'s to pin, in `:core:designsystem`.
 *
 * Here rather than in `:core:designsystem` because this module already has
 * the Robolectric Compose test dependencies.
 */
@RunWith(RobolectricTestRunner::class)
class UsThemeFollowsDeviceTest {

    @get:Rule
    val composeRule = createComposeRule()

    private class Seen {
        var ground: Color = Color.Unspecified
        var text: Color = Color.Unspecified
        var materialBackground: Color = Color.Unspecified
    }

    /** Renders `UsTheme` with [darkTheme] (null = the default) and reports what a screen inside it reads. */
    private fun render(darkTheme: Boolean? = null): Seen {
        val seen = Seen()
        composeRule.setContent {
            val read = @androidx.compose.runtime.Composable {
                seen.ground = UsTheme.extended.bgCanvas
                seen.text = UsTheme.extended.textPrimary
                seen.materialBackground = MaterialTheme.colorScheme.background
            }
            if (darkTheme == null) UsTheme { read() } else UsTheme(darkTheme = darkTheme) { read() }
        }
        composeRule.waitForIdle()
        return seen
    }

    private fun Color.isLight(): Boolean = red + green + blue > LIGHT_THRESHOLD

    @Test
    @Config(sdk = [34], qualifiers = "notnight")
    fun `on a light device a screen gets the light theme`() {
        val seen = render()

        assertThat(seen.ground.isLight()).isTrue()
        assertThat(seen.text.isLight()).isFalse()
        assertThat(seen.materialBackground).isEqualTo(seen.ground)
    }

    @Test
    @Config(sdk = [34], qualifiers = "night")
    fun `on a dark device a screen gets the dark theme`() {
        val seen = render()

        assertThat(seen.ground.isLight()).isFalse()
        assertThat(seen.text.isLight()).isTrue()
        assertThat(seen.materialBackground).isEqualTo(seen.ground)
    }

    /** The live screens pass `darkTheme = true`: a stage stays dark on a light device. */
    @Test
    @Config(sdk = [34], qualifiers = "notnight")
    fun `a screen that asks for dark gets dark on a light device`() {
        val seen = render(darkTheme = true)

        assertThat(seen.ground.isLight()).isFalse()
        assertThat(seen.text.isLight()).isTrue()
    }

    private companion object {
        /** Half of white's three channels summed: above it a colour is a light one. */
        const val LIGHT_THRESHOLD = 1.5f
    }
}
