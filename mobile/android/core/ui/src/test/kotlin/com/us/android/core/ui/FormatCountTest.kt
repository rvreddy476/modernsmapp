package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.util.Locale

class FormatCountTest {

    @Test
    fun `small counts render exactly`() {
        assertThat(formatCount(0)).isEqualTo("0")
        assertThat(formatCount(1)).isEqualTo("1")
        assertThat(formatCount(999)).isEqualTo("999")
    }

    @Test
    fun `thousands are abbreviated`() {
        assertThat(formatCount(1_000)).isEqualTo("1K")
        assertThat(formatCount(1_200)).isEqualTo("1.2K")
        assertThat(formatCount(12_400)).isEqualTo("12.4K")
    }

    @Test
    fun `millions are abbreviated`() {
        assertThat(formatCount(1_000_000)).isEqualTo("1M")
        assertThat(formatCount(3_450_000)).isEqualTo("3.4M")
    }

    /**
     * Truncation, not rounding. Rounding would render 999,999 followers as
     * "1.0M" — a number the user does not have, on the one screen where the
     * figure is the point.
     */
    @Test
    fun `boundary values truncate rather than round up`() {
        assertThat(formatCount(999_999)).isEqualTo("999.9K")
        assertThat(formatCount(1_999_999)).isEqualTo("1.9M")
    }

    /**
     * The suffixes are English, so the decimal separator must be too. On a
     * comma-decimal locale an unpinned formatter yields "1,2K", which reads as
     * twelve thousand precisely where the comma means a thousands separator.
     */
    @Test
    fun `decimal separator stays a period regardless of device locale`() {
        val original = Locale.getDefault()
        try {
            Locale.setDefault(Locale.GERMANY)
            assertThat(formatCount(1_200)).isEqualTo("1.2K")
            assertThat(formatCount(3_450_000)).isEqualTo("3.4M")
        } finally {
            Locale.setDefault(original)
        }
    }

    /** A negative count is a backend bug; it must not render as "-1". */
    @Test
    fun `negative counts clamp to zero`() {
        assertThat(formatCount(-1)).isEqualTo("0")
        assertThat(formatCount(Int.MIN_VALUE)).isEqualTo("0")
    }

    @Test
    fun `maximum int does not overflow`() {
        assertThat(formatCount(Int.MAX_VALUE)).isEqualTo("2147.4M")
    }
}
