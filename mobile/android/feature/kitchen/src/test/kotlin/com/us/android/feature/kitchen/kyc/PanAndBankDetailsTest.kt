package com.us.android.feature.kitchen.kyc

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test

/** Vectors from Architecture/shared/kyc pan_test.go, kyc_test.go and documents_test.go. Synthetic only. */
class PanAndBankDetailsTest {

    @Test
    fun `every holder type validates`() {
        val want = mapOf(
            'P' to PanHolderType.INDIVIDUAL, 'C' to PanHolderType.COMPANY, 'H' to PanHolderType.HUF,
            'F' to PanHolderType.FIRM, 'A' to PanHolderType.AOP, 'T' to PanHolderType.TRUST,
            'B' to PanHolderType.BOI, 'L' to PanHolderType.LOCAL_AUTHORITY,
            'J' to PanHolderType.ARTIFICIAL_JURIDICAL_PERSON, 'G' to PanHolderType.GOVERNMENT,
        )
        for ((code, type) in want) {
            val input = "ZZZ${code}Z0000Z"
            val check = Pan.check(input) as PanCheck.Valid
            assertThat(check.normalized).isEqualTo(input)
            assertThat(check.holderType).isEqualTo(type)
        }
        assertThat(PanHolderType.of('D')).isNull()
    }

    @Test
    fun `PAN normalises case and ASCII whitespace`() {
        for (input in listOf("zzzpz0000z", "  ZZZPZ0000Z ", "\tzzzPZ0000z\n")) {
            assertThat((Pan.check(input) as PanCheck.Valid).normalized).isEqualTo("ZZZPZ0000Z")
        }
    }

    @Test
    fun `an unknown fourth letter fails HOLDER_TYPE`() {
        for (h in "DEIKMNOQRSUVWXYZ") {
            val check = Pan.check("ZZZ${h}Z0000Z")
            assertThat((check as PanCheck.Invalid).part).isEqualTo(PanPart.HOLDER_TYPE)
        }
    }

    @Test
    fun `PAN layout failures fail FORMAT and never echo digits`() {
        val inputs = listOf(
            "", "ZZZPZ0000", "ZZZPZ0000ZZ", "ZZZP00000Z", "ZZZPZ000ZZ", "ZZZPZ00001",
            "ZZZPZ 0000Z", "1ZZPZ0000Z", "ZZZPZ0000Z1", "ZZZPſ0000Z",
        )
        for (input in inputs) {
            val check = Pan.check(input) as PanCheck.Invalid
            assertWithMessage(input).that(check.part).isEqualTo(PanPart.FORMAT)
            assertThat(check.message).doesNotContain("0000")
        }
    }

    @Test
    fun `IFSC normalises valid codes`() {
        val valid = mapOf(
            "HDFC0000053" to "HDFC0000053",
            "SBIN0001234" to "SBIN0001234",
            "ICIC0000001" to "ICIC0000001",
            "hdfc0000053" to "HDFC0000053",
            " KKBK0000958 " to "KKBK0000958",
            "UTIB0000ABC" to "UTIB0000ABC",
            "PUNB0123456" to "PUNB0123456",
        )
        for ((input, want) in valid) {
            assertWithMessage(input).that(Ifsc.normalize(input)).isEqualTo(want)
        }
    }

    @Test
    fun `IFSC refuses malformed codes`() {
        val invalid = listOf(
            "", "HDFC000053", "HDFC00000531", "HDFC1000053", "HDF00000053",
            "1DFC0000053", "HDFC0000 53", "HDFC-000053", "BAD",
        )
        for (input in invalid) {
            assertWithMessage(input).that(Ifsc.normalize(input)).isNull()
        }
    }

    @Test
    fun `bank account numbers are 9 to 18 digits and show only the last four`() {
        for (input in listOf("123456789", "765432123456789", "123456789012345678", " 123456789 ")) {
            assertWithMessage(input).that(BankAccountNumber.normalize(input)).isNotNull()
        }
        for (input in listOf("", "12345678", "1234567890123456789", "12345678a", "1234 56789", "+123456789")) {
            assertWithMessage(input).that(BankAccountNumber.normalize(input)).isNull()
        }
        assertThat(BankAccountNumber.last4("765432123456789")).isEqualTo("6789")
    }

    @Test
    fun `FSSAI licences are fourteen ASCII digits with inner spaces dropped`() {
        val ok = mapOf(
            "10099999000000" to "10099999000000",
            " 10099999000000 " to "10099999000000",
            "1009 9999 0000 00" to "10099999000000",
            "100 999 990 000 00" to "10099999000000",
        )
        for ((input, want) in ok) {
            assertWithMessage(input).that(FssaiLicence.normalize(input)).isEqualTo(want)
        }
        val bad = listOf(
            "", "1009999900000", "100999990000000", "1009999900000A", "10099999-00000",
            "100999990000٠", "ABCDEFGHIJKLMN",
        )
        for (input in bad) {
            assertWithMessage(input).that(FssaiLicence.normalize(input)).isNull()
        }
    }
}
