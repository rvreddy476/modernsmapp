package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Data saver (2026-10-24): the rule, and the ceiling it puts on the ladder. */
class DataSaverTest {

    @Test
    fun `it holds only with the switch on AND a metered network`() {
        assertThat(dataSaverActive(enabled = true, metered = true)).isTrue()
        assertThat(dataSaverActive(enabled = true, metered = false)).isFalse()
        assertThat(dataSaverActive(enabled = false, metered = true)).isFalse()
        assertThat(dataSaverActive(enabled = false, metered = false)).isFalse()
    }

    @Test
    fun `while it holds the ladder is capped at 480p`() {
        assertThat(dataSaverHeightCap(active = true, currentMaxHeight = Int.MAX_VALUE)).isEqualTo(DATA_SAVER_MAX_HEIGHT)
        assertThat(dataSaverHeightCap(active = true, currentMaxHeight = 1080)).isEqualTo(480)
        assertThat(DATA_SAVER_MAX_HEIGHT).isEqualTo(480)
    }

    @Test
    fun `it never raises a lower pick, and never touches the ladder when off`() {
        assertThat(dataSaverHeightCap(active = true, currentMaxHeight = 360)).isNull()
        assertThat(dataSaverHeightCap(active = true, currentMaxHeight = 480)).isNull()
        assertThat(dataSaverHeightCap(active = false, currentMaxHeight = Int.MAX_VALUE)).isNull()
        assertThat(NoDataSaver.active).isFalse()
    }
}
