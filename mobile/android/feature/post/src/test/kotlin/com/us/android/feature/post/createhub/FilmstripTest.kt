package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The cover picker's timeline: where the thumbnails come from, where the handle lands, what the readout says. */
class FilmstripTest {

    private val tenSeconds = 10_000_000L

    @Test
    fun `thumbnails are evenly spaced from the start and held back from the end`() {
        val stamps = Filmstrip.timestampsUs(tenSeconds, count = 4)

        assertThat(stamps).containsExactly(0L, 2_500_000L, 5_000_000L, 7_500_000L).inOrder()
    }

    @Test
    fun `the strip has two dozen thumbnails by default`() {
        assertThat(Filmstrip.timestampsUs(tenSeconds)).hasSize(Filmstrip.FRAME_COUNT)
        assertThat(Filmstrip.timestampsUs(tenSeconds).first()).isEqualTo(0L)
        assertThat(Filmstrip.timestampsUs(tenSeconds).last()).isLessThan(tenSeconds)
    }

    @Test
    fun `an unknown length puts every thumbnail at the start, and no count gives none`() {
        assertThat(Filmstrip.timestampsUs(0L, count = 3)).containsExactly(0L, 0L, 0L)
        assertThat(Filmstrip.timestampsUs(-5L, count = 2)).containsExactly(0L, 0L)
        assertThat(Filmstrip.timestampsUs(tenSeconds, count = 0)).isEmpty()
    }

    @Test
    fun `the handle maps its fraction of the strip to an instant, short of the very end`() {
        assertThat(Filmstrip.timeAt(0f, tenSeconds)).isEqualTo(0L)
        assertThat(Filmstrip.timeAt(0.5f, tenSeconds)).isEqualTo(5_000_000L)
        assertThat(Filmstrip.timeAt(1f, tenSeconds)).isEqualTo(tenSeconds - Filmstrip.TAIL_MARGIN_US)
        assertThat(Filmstrip.timeAt(1.7f, tenSeconds)).isEqualTo(tenSeconds - Filmstrip.TAIL_MARGIN_US)
        assertThat(Filmstrip.timeAt(-0.3f, tenSeconds)).isEqualTo(0L)
    }

    @Test
    fun `an instant maps back to the handle's place`() {
        assertThat(Filmstrip.fractionOf(2_500_000L, tenSeconds)).isWithin(0.0001f).of(0.25f)
        assertThat(Filmstrip.fractionOf(0L, tenSeconds)).isEqualTo(0f)
        assertThat(Filmstrip.fractionOf(5L, 0L)).isEqualTo(0f)
        assertThat(Filmstrip.fractionOf(20_000_000L, tenSeconds)).isEqualTo(1f)
    }

    @Test
    fun `the readout is minutes, seconds and tenths`() {
        assertThat(Filmstrip.format(42_600_000L)).isEqualTo("0:42.6")
        assertThat(Filmstrip.format(0L)).isEqualTo("0:00.0")
        assertThat(Filmstrip.format(65_040_000L)).isEqualTo("1:05.0")
        assertThat(Filmstrip.format(3_723_400_000L)).isEqualTo("1:02:03.4")
    }

    @Test
    fun `the readout rounds to the nearest tenth`() {
        assertThat(Filmstrip.format(42_649_999L)).isEqualTo("0:42.6")
        assertThat(Filmstrip.format(42_650_000L)).isEqualTo("0:42.7")
    }
}
