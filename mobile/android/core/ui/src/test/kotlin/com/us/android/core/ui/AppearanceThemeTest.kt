package com.us.android.core.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.junit4.createComposeRule
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.AppearanceMode
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Appearance setting through the theme (T-32, 2026-10-15), the way the
 * app's root applies it: `UsTheme(darkTheme = mode.isDark(isSystemInDarkTheme()))`.
 * System follows the device; Light and Dark hold whatever the device says.
 *
 * Here beside [UsThemeFollowsDeviceTest] for the reason it is here: this
 * module has the Robolectric Compose test dependencies.
 */
@RunWith(RobolectricTestRunner::class)
class AppearanceThemeTest {

    @get:Rule
    val composeRule = createComposeRule()

    /** The ground a screen reads under [mode], on this test's device. */
    private fun groundUnder(mode: AppearanceMode): Color {
        var ground = Color.Unspecified
        composeRule.setContent {
            UsTheme(darkTheme = mode.isDark(systemDark = isSystemInDarkTheme())) {
                ground = UsTheme.extended.bgCanvas
            }
        }
        composeRule.waitForIdle()
        return ground
    }

    private fun Color.isLight(): Boolean = red + green + blue > LIGHT_THRESHOLD

    @Test
    @Config(sdk = [34], qualifiers = "night")
    fun `System on a dark device is dark`() {
        assertThat(groundUnder(AppearanceMode.SYSTEM).isLight()).isFalse()
    }

    @Test
    @Config(sdk = [34], qualifiers = "notnight")
    fun `System on a light device is light`() {
        assertThat(groundUnder(AppearanceMode.SYSTEM).isLight()).isTrue()
    }

    @Test
    @Config(sdk = [34], qualifiers = "night")
    fun `Light is light on a dark device`() {
        assertThat(groundUnder(AppearanceMode.LIGHT).isLight()).isTrue()
    }

    @Test
    @Config(sdk = [34], qualifiers = "notnight")
    fun `Dark is dark on a light device`() {
        assertThat(groundUnder(AppearanceMode.DARK).isLight()).isFalse()
    }

    private companion object {
        /** Half of white's three channels summed: above it a colour is a light one. */
        const val LIGHT_THRESHOLD = 1.5f
    }
}
