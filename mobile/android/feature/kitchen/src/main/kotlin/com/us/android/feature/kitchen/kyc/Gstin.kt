package com.us.android.feature.kitchen.kyc

/** Which rule a GSTIN failed (shared/kyc `GSTINPart`), in the order they are checked. */
enum class GstinPart { FORMAT, STATE_CODE, PAN, CHECKSUM }

sealed interface GstinCheck {
    data class Valid(
        val normalized: String,
        val stateCode: String,
        val stateName: String,
        val pan: String,
        val panHolderType: PanHolderType,
        val entityCode: Char,
        val checkDigit: Char,
    ) : GstinCheck

    /** [message] is fixed text: never the GSTIN, its state code or its embedded PAN. */
    data class Invalid(val part: GstinPart) : GstinCheck {
        val message: String
            get() = when (part) {
                GstinPart.FORMAT ->
                    "A GSTIN is a 2-digit state code, a 10-character PAN, an entity character, Z and a check character"
                GstinPart.STATE_CODE -> "The first two digits are not an assigned GST state code"
                GstinPart.PAN -> "The PAN inside this GSTIN has an unrecognised holder type"
                GstinPart.CHECKSUM -> "This GSTIN's last character does not match — check for a typo"
            }
    }
}

/**
 * shared/kyc `ValidateGSTIN`: layout (FORMAT), state code (STATE_CODE), the
 * embedded PAN's holder type (PAN), then the mod-36 check character
 * (CHECKSUM). FORMAT and CHECKSUM only — it does not ask the GST portal whether
 * the registration exists or belongs to this business.
 */
object Gstin {
    private const val ALPHABET = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

    /** The regular (non-OIDAR, non-TDS) layout, as the server's `gstinPattern`. */
    private val PATTERN = Regex("^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$")

    fun check(input: String): GstinCheck {
        val n = normalizeAscii(input)
        if (n == null || !PATTERN.matches(n)) return GstinCheck.Invalid(GstinPart.FORMAT)
        val stateCode = n.substring(0, STATE_END)
        val stateName = GstStateCodes.nameOf(stateCode) ?: return GstinCheck.Invalid(GstinPart.STATE_CODE)
        val holder = PanHolderType.of(n[PAN_HOLDER_INDEX]) ?: return GstinCheck.Invalid(GstinPart.PAN)
        // Unreachable null after the pattern, which admits only 0-9 and A-Z.
        val expected = checkDigit(n.substring(0, BODY_LENGTH)) ?: return GstinCheck.Invalid(GstinPart.FORMAT)
        if (n[BODY_LENGTH] != expected) return GstinCheck.Invalid(GstinPart.CHECKSUM)
        return GstinCheck.Valid(
            normalized = n,
            stateCode = stateCode,
            stateName = stateName,
            pan = n.substring(STATE_END, PAN_END),
            panHolderType = holder,
            entityCode = n[PAN_END],
            checkDigit = n[BODY_LENGTH],
        )
    }

    /**
     * The fifteenth character for the first fourteen, which must already be
     * upper-case 0-9/A-Z (null otherwise — no normalisation, like the Go).
     *
     * Each character maps to 0..35; walking left to right, even indices weigh
     * 1 and odd indices 2; each product p adds p/36 + p%36; the check value is
     * (36 - sum%36) % 36.
     */
    fun checkDigit(first14: String): Char? {
        if (first14.length != BODY_LENGTH) return null
        var sum = 0
        for (i in 0 until BODY_LENGTH) {
            val value = ALPHABET.indexOf(first14[i])
            if (value < 0) return null
            val product = value * (if (i % 2 == 1) 2 else 1)
            sum += product / RADIX + product % RADIX
        }
        return ALPHABET[(RADIX - sum % RADIX) % RADIX]
    }

    private const val RADIX = 36
    private const val STATE_END = 2
    private const val PAN_HOLDER_INDEX = 5
    private const val PAN_END = 12
    private const val BODY_LENGTH = 14
}
