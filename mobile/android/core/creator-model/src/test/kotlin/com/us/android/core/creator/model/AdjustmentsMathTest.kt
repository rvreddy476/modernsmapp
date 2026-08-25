package com.us.android.core.creator.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The v2 adjustment semantics, pinned.
 *
 * The load-bearing claim is the first test: a v1 document (no saturation, no
 * warmth) must produce EXACTLY the matrix v1 produced — bit-identical floats,
 * not approximately equal — because that is what made adding channels to the
 * frozen schema safe for every already-exported picture.
 */
class AdjustmentsMathTest {

    @Suppress("MagicNumber")
    private fun v1Matrix(exposureMicros: Int, contrastMicros: Int): FloatArray {
        val exposure = exposureMicros / 1_000_000f
        val contrast = contrastMicros / 1_000_000f
        val gain = Math.pow(2.0, exposure.toDouble() * 2).toFloat()
        val slope = 1f + contrast
        val translate = 128f * (1f - slope)
        return floatArrayOf(
            gain * slope, 0f, 0f, 0f, translate,
            0f, gain * slope, 0f, 0f, translate,
            0f, 0f, gain * slope, 0f, translate,
            0f, 0f, 0f, 1f, 0f,
        )
    }

    @Test
    fun `zero saturation and warmth reproduce the v1 matrix exactly`() {
        listOf(
            Adjustments(0, 0),
            Adjustments(250_000, 60_000),
            Adjustments(-400_000, 350_000),
            Adjustments(1_000_000, -1_000_000),
        ).forEach { adjustments ->
            val v2 = AdjustmentsMath.matrix(adjustments)
            val v1 = v1Matrix(adjustments.exposureMicros, adjustments.contrastMicros)
            // Element-wise EXACT equality: identity composition must not
            // perturb a single bit.
            v2.forEachIndexed { index, value ->
                assertThat(value).isEqualTo(v1[index])
            }
        }
    }

    @Test
    fun `full desaturation weighs every input channel identically per row`() {
        val matrix = AdjustmentsMath.matrix(Adjustments(0, 0, -1_000_000, 0))
        // s = 0: every RGB row becomes the luminance vector — grey output.
        val expectedRow = floatArrayOf(0.213f, 0.715f, 0.072f)
        for (row in 0 until 3) {
            for (col in 0 until 3) {
                assertThat(matrix[row * 5 + col]).isEqualTo(expectedRow[col])
            }
        }
    }

    @Test
    fun `warmth moves red and blue in opposite directions`() {
        val warm = AdjustmentsMath.matrix(Adjustments(0, 0, 0, 1_000_000))
        val cool = AdjustmentsMath.matrix(Adjustments(0, 0, 0, -1_000_000))

        assertThat(warm[0]).isGreaterThan(1f) // red gain up
        assertThat(warm[12]).isLessThan(1f) // blue gain down
        assertThat(cool[0]).isLessThan(1f)
        assertThat(cool[12]).isGreaterThan(1f)
        // Green untouched by warmth alone.
        assertThat(warm[6]).isEqualTo(1f)
        assertThat(cool[6]).isEqualTo(1f)
    }

    @Test
    fun `channels compose in the fixed order - saturation sees contrast output`() {
        // If the order ever flipped, the translate column would differ: the
        // saturation matrix mixes the contrast translation across channels.
        val matrix = AdjustmentsMath.matrix(Adjustments(0, 500_000, 500_000, 0))
        val translate = 128f * (1f - 1.5f)
        // Saturation rows sum to 1 across RGB, so the mixed translation stays
        // the base translation — proving W∘S∘B ordering (translation flows
        // through untouched by a row-stochastic matrix).
        assertThat(matrix[4]).isWithin(1e-3f).of(translate)
        assertThat(matrix[9]).isWithin(1e-3f).of(translate)
        assertThat(matrix[14]).isWithin(1e-3f).of(translate)
    }
}
