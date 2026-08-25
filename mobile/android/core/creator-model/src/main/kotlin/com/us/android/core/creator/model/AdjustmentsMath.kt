package com.us.android.core.creator.model

/**
 * THE adjustment color matrix — one function, used by both the editor preview
 * and the render exporter.
 *
 * ## WHY THIS LIVES IN THE MODEL MODULE
 *
 * "What does exposure +0.5 look like" is document SEMANTICS, not rendering
 * detail: two renderers that disagree on it would export different pictures of
 * the same document. Before v2 the formula existed twice (preview and
 * exporter, kept identical by review); adding two more channels made textual
 * duplication a genuine drift risk, so the math moved to the one module both
 * sides already depend on. It is pure Kotlin — no android.graphics, no
 * Compose — each side wraps the returned rows in its own ColorMatrix type.
 *
 * ## THE FIXED CHANNEL ORDER
 *
 * exposure → contrast → saturation → warmth, composed as matrices in that
 * order. The order is part of the frozen semantics: saturation-then-contrast
 * is a DIFFERENT picture than contrast-then-saturation.
 *
 * ## ZERO IS EXACTLY IDENTITY
 *
 * A channel at 0 contributes the identity matrix, and multiplying by identity
 * is exact in float arithmetic — so a v1 document (both new channels absent →
 * 0) produces bit-identical pixels to what v1 produced. That property is what
 * made adding channels to the frozen schema safe.
 */
object AdjustmentsMath {

    /**
     * The combined 4x5 row-major color matrix (the android.graphics /
     * Compose ColorMatrix layout: RGBA rows, translation in the fifth column
     * on the 0..255 scale).
     */
    @Suppress("MagicNumber") // 4x5 layout indices; naming each would obscure the math.
    fun matrix(adjustments: Adjustments): FloatArray {
        val exposure = adjustments.exposureMicros / MICROS
        val contrast = adjustments.contrastMicros / MICROS
        val saturation = adjustments.saturationMicros / MICROS
        val warmth = adjustments.warmthMicros / MICROS

        // Exposure+contrast, exactly the v1 formula: gain on RGB, contrast
        // pivoting around mid-grey.
        val gain = Math.pow(2.0, exposure.toDouble() * 2).toFloat()
        val slope = 1f + contrast
        val translate = GREY_MID * (1f - slope)
        val base = floatArrayOf(
            gain * slope, 0f, 0f, 0f, translate,
            0f, gain * slope, 0f, 0f, translate,
            0f, 0f, gain * slope, 0f, translate,
            0f, 0f, 0f, 1f, 0f,
        )

        // Saturation about the BT.709-ish luminance weights the Android
        // platform matrix uses — pinned HERE so every platform computes the
        // same numbers instead of borrowing a framework implementation.
        val s = 1f + saturation
        val ir = (1f - s) * LUM_R
        val ig = (1f - s) * LUM_G
        val ib = (1f - s) * LUM_B
        val saturate = floatArrayOf(
            ir + s, ig, ib, 0f, 0f,
            ir, ig + s, ib, 0f, 0f,
            ir, ig, ib + s, 0f, 0f,
            0f, 0f, 0f, 1f, 0f,
        )

        // Warmth: a red/blue seesaw. +1 warms (red up, blue down), -1 cools.
        val warm = floatArrayOf(
            1f + warmth * WARMTH_STRENGTH, 0f, 0f, 0f, 0f,
            0f, 1f, 0f, 0f, 0f,
            0f, 0f, 1f - warmth * WARMTH_STRENGTH, 0f, 0f,
            0f, 0f, 0f, 1f, 0f,
        )

        return multiply(warm, multiply(saturate, base))
    }

    /** result = a ∘ b (apply b first, then a), in the 4x5 color-matrix algebra. */
    @Suppress("MagicNumber")
    private fun multiply(a: FloatArray, b: FloatArray): FloatArray {
        val out = FloatArray(20)
        for (row in 0 until 4) {
            val r = row * 5
            for (col in 0 until 5) {
                var value =
                    a[r] * b[col] +
                        a[r + 1] * b[5 + col] +
                        a[r + 2] * b[10 + col] +
                        a[r + 3] * b[15 + col]
                if (col == 4) value += a[r + 4]
                out[r + col] = value
            }
        }
        return out
    }

    private const val MICROS = 1_000_000f
    private const val GREY_MID = 128f

    private const val LUM_R = 0.213f
    private const val LUM_G = 0.715f
    private const val LUM_B = 0.072f

    /** ±1.0 warmth swings red/blue by a quarter — strong but not clownish. */
    private const val WARMTH_STRENGTH = 0.25f
}
