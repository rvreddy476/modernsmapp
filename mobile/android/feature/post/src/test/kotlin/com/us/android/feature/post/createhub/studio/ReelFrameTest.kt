package com.us.android.feature.post.createhub.studio

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The 9:16 frame math as a table: the crop window, its pan, the fit scale, the export size. */
class ReelFrameTest {

    private val tolerance = 0.0005f

    @Test
    fun `portrait sources default to Fill and landscape ones to Fit`() {
        assertThat(ReelFrame.defaultMode(1080, 1920)).isEqualTo(FrameMode.FILL)
        assertThat(ReelFrame.defaultMode(1080, 1080)).isEqualTo(FrameMode.FILL)
        assertThat(ReelFrame.defaultMode(1920, 1080)).isEqualTo(FrameMode.FIT)
    }

    @Test
    fun `a 16 by 9 source is wider than a reel and its window is nine sixteenths of the height`() {
        assertThat(ReelFrame.isWiderThanReel(1920, 1080)).isTrue()
        // window width = 1080 * 9/16 = 607.5 px → 607.5 / 1920 of the width
        assertThat(ReelFrame.windowFraction(1920, 1080)).isWithin(tolerance).of(607.5f / 1920f)
        assertThat(ReelFrame.fitScale(1920, 1080)).isWithin(tolerance).of(607.5f / 1920f)
    }

    @Test
    fun `a source already 9 by 16 has a full window and nothing to pan`() {
        assertThat(ReelFrame.windowFraction(1080, 1920)).isWithin(tolerance).of(1f)
        val window = ReelFrame.cropWindow(1080, 1920, pan = 1f)
        assertThat(window).isEqualTo(ReelFrame.Window(-1f, 1f, -1f, 1f))
        assertThat(ReelFrame.panAfterDrag(0.3f, dragPx = 100f, previewPx = 400f, width = 1080, height = 1920))
            .isEqualTo(0f)
    }

    @Test
    fun `the crop window is centred at pan zero and reaches the edges at plus and minus one`() {
        val fraction = ReelFrame.windowFraction(1920, 1080)

        val centre = ReelFrame.cropWindow(1920, 1080, pan = 0f)
        assertThat(centre.left).isWithin(tolerance).of(-fraction)
        assertThat(centre.right).isWithin(tolerance).of(fraction)
        assertThat(centre.bottom).isEqualTo(-1f)
        assertThat(centre.top).isEqualTo(1f)

        val right = ReelFrame.cropWindow(1920, 1080, pan = 1f)
        assertThat(right.right).isWithin(tolerance).of(1f)
        assertThat(right.left).isWithin(tolerance).of(1f - 2 * fraction)

        val left = ReelFrame.cropWindow(1920, 1080, pan = -1f)
        assertThat(left.left).isWithin(tolerance).of(-1f)

        // Out-of-range pans are clamped, never past the frame.
        assertThat(ReelFrame.cropWindow(1920, 1080, pan = 5f)).isEqualTo(right)
    }

    @Test
    fun `a tall source that is not yet 9 by 16 pans up and down instead`() {
        // 3:4 is wider than 9:16, so it pans sideways; 1:2 is narrower, so it pans up and down.
        assertThat(ReelFrame.isWiderThanReel(1080, 2160)).isFalse()
        val fraction = ReelFrame.windowFraction(1080, 2160) // 1080/(9/16) = 1920 of 2160
        assertThat(fraction).isWithin(tolerance).of(1920f / 2160f)

        val top = ReelFrame.cropWindow(1080, 2160, pan = 1f)
        assertThat(top.left).isEqualTo(-1f)
        assertThat(top.right).isEqualTo(1f)
        assertThat(top.top).isWithin(tolerance).of(1f)
        assertThat(top.bottom).isWithin(tolerance).of(1f - 2 * fraction)
    }

    @Test
    fun `dragging the picture right shows more of the left, one preview width sweeping the whole slack`() {
        // 16:9 source: slack is large. A drag of half the preview to the right moves pan by -1.
        val after = ReelFrame.panAfterDrag(pan = 0f, dragPx = 200f, previewPx = 400f, width = 1920, height = 1080)
        assertThat(after).isWithin(tolerance).of(-1f)
        val back = ReelFrame.panAfterDrag(pan = after, dragPx = -400f, previewPx = 400f, width = 1920, height = 1080)
        assertThat(back).isWithin(tolerance).of(1f)
        // Clamped at the edges.
        assertThat(ReelFrame.panAfterDrag(1f, -1_000f, 400f, 1920, 1080)).isEqualTo(1f)
        // A preview with no size changes nothing.
        assertThat(ReelFrame.panAfterDrag(0.4f, 50f, 0f, 1920, 1080)).isEqualTo(0.4f)
    }

    @Test
    fun `the export is the window's own pixels, capped at 1080 by 1920 and even`() {
        assertThat(ReelFrame.outputSize(1920, 1080)).isEqualTo(ReelFrame.Size(608, 1080))
        assertThat(ReelFrame.outputSize(1080, 1920)).isEqualTo(ReelFrame.Size(1080, 1920))
        assertThat(ReelFrame.outputSize(2160, 3840)).isEqualTo(ReelFrame.Size(1080, 1920))
        assertThat(ReelFrame.outputSize(3840, 2160)).isEqualTo(ReelFrame.Size(1080, 1920))
        assertThat(ReelFrame.outputSize(720, 1280)).isEqualTo(ReelFrame.Size(720, 1280))
        assertThat(ReelFrame.outputSize(640, 480)).isEqualTo(ReelFrame.Size(270, 480))
        val odd = ReelFrame.outputSize(1000, 999)
        assertThat(odd.width % 2).isEqualTo(0)
        assertThat(odd.height % 2).isEqualTo(0)
    }
}
