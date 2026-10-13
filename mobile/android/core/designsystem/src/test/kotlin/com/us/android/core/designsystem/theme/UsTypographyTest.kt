package com.us.android.core.designsystem.theme

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class UsTypographyTest {
    @Test
    fun `every Material type role uses a bundled brand font`() {
        val t = UsTypography
        val styles = listOf(
            t.displayLarge, t.displayMedium, t.displaySmall,
            t.headlineLarge, t.headlineMedium, t.headlineSmall,
            t.titleLarge, t.titleMedium, t.titleSmall,
            t.bodyLarge, t.bodyMedium, t.bodySmall,
            t.labelLarge, t.labelMedium, t.labelSmall,
        )
        styles.forEach { assertThat(it.fontFamily).isAnyOf(OutfitFontFamily, FigtreeFontFamily) }
    }

    @Test
    fun `body copy remains readable and every line has breathing room`() {
        val t = UsTypography
        assertThat(t.bodyLarge.fontSize.value).isAtLeast(16f)
        assertThat(t.bodyMedium.fontSize.value).isAtLeast(14f)
        listOf(t.bodyLarge, t.bodyMedium, t.bodySmall, t.titleMedium).forEach {
            assertThat(it.lineHeight.value).isGreaterThan(it.fontSize.value)
        }
    }
}
