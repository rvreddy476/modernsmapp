package com.us.android.feature.kitchen.kyc

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test

/**
 * The Go tests' vectors (Architecture/shared/kyc/gstin_test.go), case for case.
 *
 * [refCheck] is an INDEPENDENT mod-36 used only to build vectors: it walks right
 * to left from weight 2, where production walks left to right from weight 1, so
 * a mutation in production cannot move the expected values. Every identifier
 * here is synthetic.
 */
class GstinTest {

    private fun refCheck(first14: String): Char {
        val alpha = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
        require(first14.length == 14) { "refCheck needs 14 characters" }
        var weight = 2
        var sum = 0
        for (i in 13 downTo 0) {
            val cp = alpha.indexOf(first14[i])
            require(cp >= 0) { "bad character at $i" }
            val d = weight * cp
            sum += d / 36 + d % 36
            weight = if (weight == 2) 1 else 2
        }
        return alpha[(36 - sum % 36) % 36]
    }

    private fun synth(state: String, pan: String): String {
        val base = state + pan + "1Z"
        return base + refCheck(base)
    }

    private fun partOf(check: GstinCheck): GstinPart =
        (check as? GstinCheck.Invalid)?.part ?: throw AssertionError("expected Invalid, got $check")

    private fun assertNoEcho(check: GstinCheck, input: String) {
        val message = (check as? GstinCheck.Invalid)?.message ?: return
        val n = input.trim().uppercase()
        if (n.length >= 6) assertThat(message).doesNotContain(n)
        if (n.length >= 12) assertThat(message).doesNotContain(n.substring(2, 12))
    }

    // 2 9 Z Z Z P Z 0 0 0 0 Z 1 Z -> digit sums 246, 246 mod 36 = 30, check 6.
    @Test
    fun `the hand-computed golden 29ZZZPZ0000Z1Z6 validates into its parts`() {
        val golden = "29ZZZPZ0000Z1Z6"
        assertThat(refCheck(golden.take(14))).isEqualTo('6')
        assertThat(Gstin.checkDigit(golden.take(14))).isEqualTo('6')

        val valid = Gstin.check(golden) as GstinCheck.Valid
        assertThat(valid.normalized).isEqualTo(golden)
        assertThat(valid.stateCode).isEqualTo("29")
        assertThat(valid.stateName).isEqualTo("Karnataka")
        assertThat(valid.pan).isEqualTo("ZZZPZ0000Z")
        assertThat(valid.panHolderType).isEqualTo(PanHolderType.INDIVIDUAL)
        assertThat(valid.entityCode).isEqualTo('1')
        assertThat(valid.checkDigit).isEqualTo('6')
    }

    @Test
    fun `every assigned state code validates`() {
        val codes = (1..38).map { it.toString().padStart(2, '0') } + "97"
        for (code in codes) {
            val check = Gstin.check(synth(code, "ZZZCZ0000Z"))
            assertThat(check).isInstanceOf(GstinCheck.Valid::class.java)
            check as GstinCheck.Valid
            assertThat(check.stateCode).isEqualTo(code)
            assertThat(check.pan).isEqualTo("ZZZCZ0000Z")
            assertThat(check.panHolderType).isEqualTo(PanHolderType.COMPANY)
            assertThat(GstStateCodes.isAssigned(code)).isTrue()
            assertThat(GstStateCodes.nameOf(code)).isNotNull()
        }
    }

    @Test
    fun `lower case and surrounding ASCII whitespace normalise`() {
        val want = synth("27", "ZZZFZ0000Z")
        for (input in listOf(want.lowercase(), "  $want\t", " ${want.lowercase()}\n")) {
            assertThat((Gstin.check(input) as GstinCheck.Valid).normalized).isEqualTo(want)
        }
    }

    @Test
    fun `unassigned state codes fail STATE_CODE`() {
        for (code in listOf("00", "39", "40", "96", "98", "99")) {
            val input = synth(code, "ZZZPZ0000Z")
            val check = Gstin.check(input)
            assertThat(partOf(check)).isEqualTo(GstinPart.STATE_CODE)
            assertNoEcho(check, input)
            assertThat(GstStateCodes.isAssigned(code)).isFalse()
        }
    }

    @Test
    fun `every wrong check character fails CHECKSUM`() {
        val valid = synth("29", "ZZZHZ0000Z")
        val alpha = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
        var wrong = 0
        for (c in alpha) {
            val input = valid.take(14) + c
            val check = Gstin.check(input)
            if (c == valid[14]) {
                assertThat(check).isInstanceOf(GstinCheck.Valid::class.java)
                continue
            }
            wrong++
            assertThat(partOf(check)).isEqualTo(GstinPart.CHECKSUM)
            assertNoEcho(check, input)
        }
        assertThat(wrong).isEqualTo(35)
    }

    @Test
    fun `layout failures fail FORMAT`() {
        val valid = synth("29", "ZZZPZ0000Z")
        val cases = mapOf(
            "fourteenth not Z" to valid.substring(0, 13) + "Y" + valid.substring(14),
            "entity code zero" to valid.substring(0, 12) + "0" + valid.substring(13),
            "inner space" to valid.substring(0, 7) + " " + valid.substring(8),
            "short" to valid.take(14),
            "long" to valid + "1",
            "empty" to "",
            "letter in state" to "2A" + valid.substring(2),
            "digit in PAN name" to valid.take(2) + "1" + valid.substring(3),
            "non-ASCII long s" to valid.take(2) + "ſ" + valid.substring(3),
            "symbol" to valid.take(14) + "#",
        )
        for ((name, input) in cases) {
            val check = Gstin.check(input)
            assertThat(check).isInstanceOf(GstinCheck.Invalid::class.java)
            assertWithMessage(name).that(partOf(check)).isEqualTo(GstinPart.FORMAT)
            assertNoEcho(check, input)
        }
    }

    @Test
    fun `an embedded PAN with no holder type fails PAN even with a valid checksum`() {
        val base = "29ABCDE1234F1Z"
        val input = base + refCheck(base)
        assertThat(input).isEqualTo("29ABCDE1234F1ZW")
        assertThat(partOf(Gstin.check(input))).isEqualTo(GstinPart.PAN)
        for (bad in listOf('D', 'E', 'K', 'Z', 'X')) {
            val b = "29ZZZ${bad}Z0000Z1Z"
            assertThat(partOf(Gstin.check(b + refCheck(b)))).isEqualTo(GstinPart.PAN)
        }
    }

    @Test
    fun `checkDigit refuses anything but fourteen upper-case alphanumerics`() {
        for (input in listOf("", "29ZZZPZ0000Z1", "29ZZZPZ0000Z1Z6", "29zzzpz0000z1z", "29ZZZPZ0000Z1#")) {
            assertThat(Gstin.checkDigit(input)).isNull()
        }
    }

    @Test
    fun `state names are exact two-character lookups`() {
        assertThat(GstStateCodes.nameOf("29")).isEqualTo("Karnataka")
        assertThat(GstStateCodes.nameOf("97")).isEqualTo("Other Territory")
        for (code in listOf("", "0", "00", "39", "99", " 29", "29 ")) {
            assertThat(GstStateCodes.nameOf(code)).isNull()
        }
    }

    @Test
    fun `the location picker offers food-service's state names without 97 and 28`() {
        val names = GstStateCodes.locationStateNames
        assertThat(names).contains("Karnataka")
        assertThat(names).contains("Andhra Pradesh")
        assertThat(names).doesNotContain("Other Territory")
        assertThat(names).doesNotContain("Andhra Pradesh (before reorganisation)")
        assertThat(names).hasSize(37)
        assertThat(names).isInOrder()
    }
}
