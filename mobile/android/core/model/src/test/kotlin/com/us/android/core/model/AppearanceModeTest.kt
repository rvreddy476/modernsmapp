package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The Appearance setting's mapping to a theme (T-32): System follows the device, Light and Dark force it. */
class AppearanceModeTest {

    @Test
    fun `System follows the device`() {
        assertThat(AppearanceMode.SYSTEM.isDark(systemDark = true)).isTrue()
        assertThat(AppearanceMode.SYSTEM.isDark(systemDark = false)).isFalse()
    }

    @Test
    fun `Light is light on a dark device, and Dark is dark on a light one`() {
        assertThat(AppearanceMode.LIGHT.isDark(systemDark = true)).isFalse()
        assertThat(AppearanceMode.LIGHT.isDark(systemDark = false)).isFalse()
        assertThat(AppearanceMode.DARK.isDark(systemDark = false)).isTrue()
        assertThat(AppearanceMode.DARK.isDark(systemDark = true)).isTrue()
    }

    @Test
    fun `the stored value round-trips, and nothing or junk stored is System`() {
        AppearanceMode.entries.forEach { assertThat(AppearanceMode.fromWire(it.wire)).isEqualTo(it) }
        assertThat(AppearanceMode.fromWire(null)).isEqualTo(AppearanceMode.SYSTEM)
        assertThat(AppearanceMode.fromWire("sepia")).isEqualTo(AppearanceMode.SYSTEM)
    }

    @Test
    fun `the three choices read System, Light, Dark in that order`() {
        assertThat(AppearanceMode.entries.map { it.label }).containsExactly("System", "Light", "Dark").inOrder()
    }
}
