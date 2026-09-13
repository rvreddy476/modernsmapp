package com.us.android.feature.rider.kyc

/** A validated, normalised driving-licence number (shared/kyc `DrivingLicence`). */
data class DrivingLicenceNumber(
    /** The compact 15-character form, e.g. KA0120200000001. */
    val normalized: String,
    val stateCode: String,
    val rtoCode: String,
    val year: String,
    val serial: String,
)

/**
 * shared/kyc `ValidateDrivingLicence`: upper-case, drop spaces, hyphens and
 * slashes, then the 15-character Sarathi layout — state letters, two-digit RTO,
 * a 19xx/20xx year of issue, a seven-digit serial — with a known state prefix.
 *
 * FORMAT ONLY. Nothing here asks Sarathi or DigiLocker whether it exists.
 */
object DrivingLicence {
    private val PATTERN = Regex("^([A-Z]{2})([0-9]{2})((?:19|20)[0-9]{2})([0-9]{7})$")

    fun parse(input: String): DrivingLicenceNumber? {
        val n = normalizeAscii(input, drop = " -/") ?: return null
        val m = PATTERN.matchEntire(n) ?: return null
        val (state, rto, year, serial) = m.destructured
        if (state !in RTO_STATE_CODES) return null
        return DrivingLicenceNumber(normalized = n, stateCode = state, rtoCode = rto, year = year, serial = serial)
    }

    const val INVALID_MESSAGE = "A driving licence number is 15 characters, like KA0120200000001"
}

enum class VehicleSeries { STATE, BH }

/** A validated, normalised registration mark (shared/kyc `VehicleRegistration`). */
data class VehicleRegistrationNumber(
    val normalized: String,
    val series: VehicleSeries,
    /** STATE series only. */
    val stateCode: String = "",
    val rtoCode: String = "",
    val letters: String = "",
    val number: String = "",
    /** Two-digit year of registration, BH series only. */
    val year: String = "",
)

/**
 * shared/kyc `ValidateVehicleRegistration`: upper-case, drop spaces, hyphens and
 * dots, then either a state-series mark (MH12ZZ0000, DL3CZZ0000) with a known
 * state prefix, or a Bharat-series mark (22BH0000ZZ).
 *
 * FORMAT ONLY. It does not ask VAHAN about the vehicle.
 */
object VehicleRegistration {
    private val STATE = Regex("^([A-Z]{2})([0-9]{1,2})([A-Z]{0,3})([0-9]{1,4})$")
    private val BH = Regex("^([0-9]{2})BH([0-9]{4})([A-Z]{1,2})$")

    fun parse(input: String): VehicleRegistrationNumber? {
        val n = normalizeAscii(input, drop = " -.") ?: return null
        BH.matchEntire(n)?.let { m ->
            val (year, number, letters) = m.destructured
            return VehicleRegistrationNumber(normalized = n, series = VehicleSeries.BH, year = year, number = number, letters = letters)
        }
        val m = STATE.matchEntire(n) ?: return null
        val (state, rto, letters, number) = m.destructured
        if (state !in RTO_STATE_CODES) return null
        return VehicleRegistrationNumber(
            normalized = n,
            series = VehicleSeries.STATE,
            stateCode = state,
            rtoCode = rto,
            letters = letters,
            number = number,
        )
    }

    const val INVALID_MESSAGE = "A registration number looks like MH12AB1234 or 22BH1234AB"
}
