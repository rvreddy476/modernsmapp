package com.us.android.feature.rider.kyc

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test

/** The vectors are Architecture/shared/kyc/documents_test.go's, verbatim. All numbers are synthetic. */
class DrivingDocumentsTest {

    @Test
    fun `driving licence accepts the Sarathi layout with any separator`() {
        val want = DrivingLicenceNumber(normalized = "KA0120200000001", stateCode = "KA", rtoCode = "01", year = "2020", serial = "0000001")
        for (input in listOf("KA0120200000001", "KA01 20200000001", "KA-01-2020-0000001", "ka01/2020/0000001", " ka01 2020 0000001 ")) {
            assertThat(DrivingLicence.parse(input)).isEqualTo(want)
        }
        assertThat(DrivingLicence.parse("OD0219990000009")?.year).isEqualTo("1999")
    }

    @Test
    fun `driving licence refuses everything the Go validator refuses`() {
        val refused = mapOf(
            "unknown state" to "ZZ0120200000001",
            "year 18xx" to "KA0118990000001",
            "year 21xx" to "KA0121000000001",
            "short serial" to "KA012020000001",
            "long serial" to "KA01202000000011",
            "letter serial" to "KA012020000000A",
            "empty" to "",
            "old layout" to "KA01 2020 000001",
            "underscore sep" to "KA01_20200000001",
            "non-ASCII look-alike" to "KA0120200000001ı",
        )
        for ((name, input) in refused) {
            assertWithMessage(name).that(DrivingLicence.parse(input)).isNull()
        }
    }

    @Test
    fun `vehicle registration accepts the state series`() {
        val state = mapOf(
            "MH12ZZ0000" to VehicleRegistrationNumber("MH12ZZ0000", VehicleSeries.STATE, stateCode = "MH", rtoCode = "12", letters = "ZZ", number = "0000"),
            "mh 12 zz 0000" to VehicleRegistrationNumber("MH12ZZ0000", VehicleSeries.STATE, stateCode = "MH", rtoCode = "12", letters = "ZZ", number = "0000"),
            "MH-12-ZZ-0000" to VehicleRegistrationNumber("MH12ZZ0000", VehicleSeries.STATE, stateCode = "MH", rtoCode = "12", letters = "ZZ", number = "0000"),
            "DL3CZZ0000" to VehicleRegistrationNumber("DL3CZZ0000", VehicleSeries.STATE, stateCode = "DL", rtoCode = "3", letters = "CZZ", number = "0000"),
            "KA.01.Z.0001" to VehicleRegistrationNumber("KA01Z0001", VehicleSeries.STATE, stateCode = "KA", rtoCode = "01", letters = "Z", number = "0001"),
        )
        for ((input, want) in state) {
            assertWithMessage(input).that(VehicleRegistration.parse(input)).isEqualTo(want)
        }
    }

    @Test
    fun `vehicle registration accepts the BH series`() {
        assertThat(VehicleRegistration.parse("22BH0000ZZ"))
            .isEqualTo(VehicleRegistrationNumber("22BH0000ZZ", VehicleSeries.BH, year = "22", number = "0000", letters = "ZZ"))
        assertThat(VehicleRegistration.parse("22 bh 0000 z"))
            .isEqualTo(VehicleRegistrationNumber("22BH0000Z", VehicleSeries.BH, year = "22", number = "0000", letters = "Z"))
    }

    @Test
    fun `vehicle registration refuses everything the Go validator refuses`() {
        val refused = mapOf(
            "unknown state" to "ZZ12ZZ0000",
            "five-digit number" to "MH12ZZ00000",
            "four letters" to "MH12ZZZZ0000",
            "no number" to "MH12ZZ",
            "BH no letters" to "22BH0000",
            "BH three letters" to "22BH0000ZZZ",
            "BH three-digit" to "22BH000ZZ",
            "BH one-digit year" to "2BH0000ZZ",
            "empty" to "",
            "slash separator" to "MH12/ZZ/0000",
        )
        for ((name, input) in refused) {
            assertWithMessage(name).that(VehicleRegistration.parse(input)).isNull()
        }
    }

    @Test
    fun `bank details keep the kitchen's rules`() {
        assertThat(Ifsc.normalize(" sbin0001234 ")).isEqualTo("SBIN0001234")
        assertThat(Ifsc.normalize("SBIN1001234")).isNull()
        assertThat(BankAccountNumber.normalize("123456789")).isEqualTo("123456789")
        assertThat(BankAccountNumber.normalize("12345678")).isNull()
        assertThat(BankAccountNumber.normalize("1234567890123456789")).isNull()
    }
}
