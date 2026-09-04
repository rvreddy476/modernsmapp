package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The decoder against the reference hash, and its refusals. */
class BlurHashTest {

    /** The BlurHash README's own example: a 4×3 component hash. */
    private val reference = "LEHV6nWB2yk8pyo0adR*.7kCMdnj"

    @Test
    fun `decodes the reference hash to opaque pixels of the asked size`() {
        val pixels = BlurHash.decode(reference, 32, 18)

        assertThat(pixels).isNotNull()
        assertThat(pixels!!).hasLength(32 * 18)
        assertThat(pixels.all { (it ushr 24) == 0xFF }).isTrue()
        // Not one flat colour: the components carry a gradient.
        assertThat(pixels.distinct().size).isGreaterThan(1)
    }

    @Test
    fun `the average colour comes from the DC term`() {
        // The DC of the reference is a warm mid-tone; every channel lands in the middle of the range.
        val pixels = BlurHash.decode(reference, 8, 8)!!
        val r = pixels.map { (it shr 16) and 0xFF }.average()
        val g = pixels.map { (it shr 8) and 0xFF }.average()
        val b = pixels.map { it and 0xFF }.average()
        assertThat(r).isIn(com.google.common.collect.Range.closed(60.0, 200.0))
        assertThat(g).isIn(com.google.common.collect.Range.closed(60.0, 200.0))
        assertThat(b).isIn(com.google.common.collect.Range.closed(40.0, 200.0))
    }

    @Test
    fun `a bad hash decodes to nothing rather than throwing`() {
        assertThat(BlurHash.decode("", 4, 4)).isNull()
        assertThat(BlurHash.decode("LEHV6", 4, 4)).isNull()
        assertThat(BlurHash.decode("LEHV6nWB2yk8pyo0adR*.7kCMdn", 4, 4)).isNull() // one short
        assertThat(BlurHash.decode("LEHV6nWB2yk8pyo0adR*.7kCMdné", 4, 4)).isNull() // not in the alphabet
        assertThat(BlurHash.decode(reference, 0, 4)).isNull()
    }
}
