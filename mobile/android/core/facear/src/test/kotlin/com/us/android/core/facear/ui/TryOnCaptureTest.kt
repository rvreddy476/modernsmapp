package com.us.android.core.facear.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The one decision in the capture path that is not a bitmap operation.
 *
 * A shared try-on picture is a product recommendation sent to a friend, and
 * the caption is the only thing that makes it one rather than a photo of
 * somebody's face. The drawing is untestable here — it is a `Canvas` — so the
 * part that decides WHAT is drawn is a pure function, and this is it.
 */
class TryOnCaptureTest {

    @Test
    fun `the caption is the product and the shade`() {
        assertThat(captureCaption("Velvet Matte Lipstick", "Crimson"))
            .isEqualTo("Velvet Matte Lipstick — Crimson")
    }

    @Test
    fun `a product with no shade is still a caption`() {
        // The try-on may be running on the bundle's own default, and the
        // product name alone is still worth burning in.
        assertThat(captureCaption("Velvet Matte Lipstick", null))
            .isEqualTo("Velvet Matte Lipstick")
    }

    @Test
    fun `a blank shade label does not produce a dangling separator`() {
        listOf("", "   ", "\n").forEach { blank ->
            assertThat(captureCaption("Velvet Matte Lipstick", blank))
                .isEqualTo("Velvet Matte Lipstick")
        }
    }

    @Test
    fun `an empty product and shade produce nothing to stamp`() {
        // stampCapture returns the picture untouched for a blank caption
        // rather than darkening it with an empty plate.
        assertThat(captureCaption("  ", null)).isEmpty()
        assertThat(captureCaption("", "")).isEmpty()
    }

    @Test
    fun `surrounding whitespace off the wire is trimmed, not rendered`() {
        assertThat(captureCaption("  Velvet Matte  ", "  Crimson  "))
            .isEqualTo("Velvet Matte — Crimson")
    }

    @Test
    fun `an en dash separates, so a product title containing commas still reads`() {
        assertThat(captureCaption("Lipstick, Matte, Long-wear", "Rose"))
            .isEqualTo("Lipstick, Matte, Long-wear — Rose")
    }
}
