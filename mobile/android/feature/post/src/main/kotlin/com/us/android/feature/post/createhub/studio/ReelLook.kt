package com.us.android.feature.post.createhub.studio

/**
 * The studio's looks (2026-09-05): seven named colour grades, each ONE
 * 4×4 matrix over (r, g, b, 1) — which is exactly what Media3's
 * `RgbMatrix` shader computes (`uRgbMatrix * vec4(rgb, 1)`), so the fourth
 * column carries an offset and a look can lift blacks or warm shadows
 * without a LUT. The same matrix drives the preview player, the export and
 * the row of live thumbnails, so what the user picks is what is posted.
 *
 * Pure: [matrix] is row-major (as written), [glMatrix] is the column-major
 * copy OpenGL wants, and [colorMatrix] is Compose's 4×5 with the offsets in
 * 0..255 for the thumbnail row. The parameter table is a test.
 */
@Suppress("MagicNumber") // The table IS the grades: each number is a look's parameter, named by its row.
enum class ReelLook(val label: String, val matrix: FloatArray) {
    NONE("None", identity()),
    WARM("Warm", tint(red = 1.08f, green = 1.02f, blue = 0.90f, offset = floatArrayOf(0.02f, 0.0f, -0.02f))),
    COOL("Cool", tint(red = 0.92f, green = 1.0f, blue = 1.10f, offset = floatArrayOf(-0.02f, 0.0f, 0.03f))),
    VIVID("Vivid", multiply(contrast(1.08f), saturation(1.35f))),
    FADE("Fade", multiply(lift(0.08f), multiply(contrast(0.85f), saturation(0.85f)))),
    MONO("Mono", saturation(0f)),
    NOIR("Noir", multiply(lift(-0.03f), multiply(contrast(1.35f), saturation(0f)))),
    ;

    /** The 16 values column-major, the way `glUniformMatrix4fv` reads them without transposing. */
    fun glMatrix(): FloatArray = FloatArray(MATRIX_SIZE) { index ->
        val column = index / DIMENSION
        val row = index % DIMENSION
        matrix[row * DIMENSION + column]
    }

    /**
     * Compose's `ColorMatrix` layout: 4 rows × 5 columns, row-major, the
     * fifth column the offset in 0..255. The alpha row is identity.
     */
    fun colorMatrix(): FloatArray {
        val out = FloatArray(COLOR_MATRIX_SIZE)
        for (row in 0 until CHANNELS) {
            for (column in 0 until CHANNELS) out[row * COLOR_COLUMNS + column] = matrix[row * DIMENSION + column]
            out[row * COLOR_COLUMNS + ALPHA] = 0f
            out[row * COLOR_COLUMNS + OFFSET] = matrix[row * DIMENSION + OFFSET_COLUMN] * BYTE
        }
        out[ALPHA * COLOR_COLUMNS + ALPHA] = 1f
        return out
    }

    /** The colour this look gives (r, g, b in 0..1) — what the tests and a thumbnail see. */
    fun apply(red: Float, green: Float, blue: Float): FloatArray = FloatArray(CHANNELS) { row ->
        val base = row * DIMENSION
        (matrix[base] * red + matrix[base + 1] * green + matrix[base + 2] * blue + matrix[base + OFFSET_COLUMN])
            .coerceIn(0f, 1f)
    }

    companion object {
        private const val DIMENSION = 4
        private const val MATRIX_SIZE = DIMENSION * DIMENSION
        private const val CHANNELS = 3
        private const val ALPHA = 3
        private const val OFFSET = 4
        private const val OFFSET_COLUMN = 3
        private const val COLOR_COLUMNS = 5
        private const val COLOR_MATRIX_SIZE = DIMENSION * COLOR_COLUMNS
        private const val BYTE = 255f
    }
}

// ── The building blocks, row-major 4×4 over (r, g, b, 1) ─────────────────

private const val LUMA_R = 0.2126f
private const val LUMA_G = 0.7152f
private const val LUMA_B = 0.0722f
private const val HALF = 0.5f

private fun identity(): FloatArray = floatArrayOf(
    1f, 0f, 0f, 0f,
    0f, 1f, 0f, 0f,
    0f, 0f, 1f, 0f,
    0f, 0f, 0f, 1f,
)

/** Scales each channel and adds a small offset — a warm or cool cast. */
private fun tint(red: Float, green: Float, blue: Float, offset: FloatArray): FloatArray = floatArrayOf(
    red, 0f, 0f, offset[0],
    0f, green, 0f, offset[1],
    0f, 0f, blue, offset[2],
    0f, 0f, 0f, 1f,
)

/** Blends each channel toward the pixel's luminance: 0 is grey, 1 is unchanged, above 1 is more colour. */
private fun saturation(amount: Float): FloatArray {
    val inverse = 1f - amount
    val r = LUMA_R * inverse
    val g = LUMA_G * inverse
    val b = LUMA_B * inverse
    return floatArrayOf(
        r + amount, g, b, 0f,
        r, g + amount, b, 0f,
        r, g, b + amount, 0f,
        0f, 0f, 0f, 1f,
    )
}

/** Stretches around mid-grey: `c·(x − 0.5) + 0.5`. */
private fun contrast(amount: Float): FloatArray {
    val offset = HALF * (1f - amount)
    return floatArrayOf(
        amount, 0f, 0f, offset,
        0f, amount, 0f, offset,
        0f, 0f, amount, offset,
        0f, 0f, 0f, 1f,
    )
}

/** Adds the same amount to every channel — lifted blacks (positive) or crushed ones (negative). */
private fun lift(amount: Float): FloatArray = floatArrayOf(
    1f, 0f, 0f, amount,
    0f, 1f, 0f, amount,
    0f, 0f, 1f, amount,
    0f, 0f, 0f, 1f,
)

private const val N = 4

/** `a × b` — applies b first, then a. */
private fun multiply(a: FloatArray, b: FloatArray): FloatArray {
    val out = FloatArray(N * N)
    for (row in 0 until N) {
        for (column in 0 until N) {
            var sum = 0f
            for (k in 0 until N) sum += a[row * N + k] * b[k * N + column]
            out[row * N + column] = sum
        }
    }
    return out
}
