package com.us.android.feature.kitchen.kyc

/** The fourth character of a PAN (shared/kyc `PANHolderType`). */
enum class PanHolderType(val code: Char, val label: String) {
    INDIVIDUAL('P', "Individual"),
    COMPANY('C', "Company"),
    HUF('H', "Hindu undivided family"),
    FIRM('F', "Firm"),
    AOP('A', "Association of persons"),
    TRUST('T', "Trust"),
    BOI('B', "Body of individuals"),
    LOCAL_AUTHORITY('L', "Local authority"),
    ARTIFICIAL_JURIDICAL_PERSON('J', "Artificial juridical person"),
    GOVERNMENT('G', "Government"),
    ;

    companion object {
        fun of(code: Char): PanHolderType? = entries.firstOrNull { it.code == code }
    }
}

/** Which rule a PAN failed (shared/kyc `PANPart`). */
enum class PanPart { FORMAT, HOLDER_TYPE }

sealed interface PanCheck {
    data class Valid(val normalized: String, val holderType: PanHolderType) : PanCheck

    /** [message] is fixed text and never contains the value checked. */
    data class Invalid(val part: PanPart) : PanCheck {
        val message: String
            get() = when (part) {
                PanPart.FORMAT -> "A PAN is five letters, four digits and a letter"
                PanPart.HOLDER_TYPE -> "The fourth letter of this PAN is not a recognised holder type"
            }
    }
}

/**
 * shared/kyc `ValidatePAN`: trim, upper-case (ASCII only), layout, holder type.
 * FORMAT only — nothing asks the Income Tax Department whether it exists.
 */
object Pan {
    private val PATTERN = Regex("^[A-Z]{5}[0-9]{4}[A-Z]$")

    fun check(input: String): PanCheck {
        val normalized = normalizeAscii(input)
        if (normalized == null || !PATTERN.matches(normalized)) return PanCheck.Invalid(PanPart.FORMAT)
        val holder = PanHolderType.of(normalized[HOLDER_INDEX]) ?: return PanCheck.Invalid(PanPart.HOLDER_TYPE)
        return PanCheck.Valid(normalized, holder)
    }

    private const val HOLDER_INDEX = 3
}
