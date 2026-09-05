package com.us.android.feature.post.createhub.studio

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The looks' parameter table: what each matrix does to a colour, and how it is laid out for GL and Compose. */
class ReelLookTest {

    private val tolerance = 0.002f

    @Test
    fun `there are seven looks in the founder's order and every matrix is four by four`() {
        assertThat(ReelLook.entries.map { it.label })
            .containsExactly("None", "Warm", "Cool", "Vivid", "Fade", "Mono", "Noir")
            .inOrder()
        ReelLook.entries.forEach { look ->
            assertThat(look.matrix).hasLength(16)
            assertThat(look.glMatrix()).hasLength(16)
            assertThat(look.colorMatrix()).hasLength(20)
            // The homogeneous row is always (0, 0, 0, 1).
            assertThat(look.matrix.copyOfRange(12, 16).toList()).containsExactly(0f, 0f, 0f, 1f).inOrder()
        }
    }

    @Test
    fun `None leaves a colour alone`() {
        val out = ReelLook.NONE.apply(0.2f, 0.5f, 0.8f)
        assertThat(out[0]).isWithin(tolerance).of(0.2f)
        assertThat(out[1]).isWithin(tolerance).of(0.5f)
        assertThat(out[2]).isWithin(tolerance).of(0.8f)
    }

    @Test
    fun `Mono and Noir make every colour grey, and Noir has more contrast`() {
        val mono = ReelLook.MONO.apply(0.9f, 0.2f, 0.1f)
        assertThat(mono[0]).isWithin(tolerance).of(mono[1])
        assertThat(mono[1]).isWithin(tolerance).of(mono[2])

        val noir = ReelLook.NOIR.apply(0.9f, 0.2f, 0.1f)
        assertThat(noir[0]).isWithin(tolerance).of(noir[1])
        // A dark colour gets darker under Noir than under Mono; a light one lighter.
        val darkMono = ReelLook.MONO.apply(0.2f, 0.2f, 0.2f)[0]
        val darkNoir = ReelLook.NOIR.apply(0.2f, 0.2f, 0.2f)[0]
        val lightMono = ReelLook.MONO.apply(0.8f, 0.8f, 0.8f)[0]
        val lightNoir = ReelLook.NOIR.apply(0.8f, 0.8f, 0.8f)[0]
        assertThat(darkNoir).isLessThan(darkMono)
        assertThat(lightNoir).isGreaterThan(lightMono)
    }

    @Test
    fun `Warm lifts red over blue and Cool does the opposite`() {
        val grey = floatArrayOf(0.5f, 0.5f, 0.5f)
        val warm = ReelLook.WARM.apply(grey[0], grey[1], grey[2])
        val cool = ReelLook.COOL.apply(grey[0], grey[1], grey[2])
        assertThat(warm[0]).isGreaterThan(warm[2])
        assertThat(cool[2]).isGreaterThan(cool[0])
    }

    @Test
    fun `Vivid pushes a colour further from grey and Fade pulls it closer and lifts black`() {
        val vivid = ReelLook.VIVID.apply(0.8f, 0.3f, 0.3f)
        val fade = ReelLook.FADE.apply(0.8f, 0.3f, 0.3f)
        assertThat(vivid[0] - vivid[1]).isGreaterThan(0.5f)
        assertThat(fade[0] - fade[1]).isLessThan(0.5f)
        // Black is no longer black under Fade — the lift.
        assertThat(ReelLook.FADE.apply(0f, 0f, 0f)[0]).isGreaterThan(0f)
        assertThat(ReelLook.NONE.apply(0f, 0f, 0f)[0]).isEqualTo(0f)
    }

    @Test
    fun `the GL matrix is the column-major transpose with the offsets in the last column`() {
        val row = ReelLook.FADE.matrix
        val gl = ReelLook.FADE.glMatrix()
        // Row-major (r, c) lands at column-major index c * 4 + r.
        for (r in 0 until 4) for (c in 0 until 4) assertThat(gl[c * 4 + r]).isEqualTo(row[r * 4 + c])
        // The offsets — row-major column 3 — sit at GL indices 12, 13, 14.
        assertThat(gl[12]).isEqualTo(row[3])
        assertThat(gl[13]).isEqualTo(row[7])
        assertThat(gl[14]).isEqualTo(row[11])
    }

    @Test
    fun `the Compose matrix scales the offsets to bytes and keeps alpha`() {
        val compose = ReelLook.FADE.colorMatrix()
        val row = ReelLook.FADE.matrix
        assertThat(compose[4]).isWithin(tolerance).of(row[3] * 255f)
        assertThat(compose[9]).isWithin(tolerance).of(row[7] * 255f)
        assertThat(compose[14]).isWithin(tolerance).of(row[11] * 255f)
        assertThat(compose[18]).isEqualTo(1f)
        assertThat(compose[19]).isEqualTo(0f)
        assertThat(compose[3]).isEqualTo(0f)
    }
}
