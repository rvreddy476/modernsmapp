package com.us.android.feature.rider.kyc

/*
 * Client-side format checks for the identifiers rider verification captures.
 *
 * Each validator MIRRORS Architecture/shared/kyc (Go), rule for rule, and its
 * tests carry the Go tests' vectors. They are a courtesy, not a gate: the server
 * re-validates everything (riderkyc.ValidateDocument).
 *
 * DUPLICATED from :feature:kitchen's kyc/KycText.kt (normalizeAscii). Lift both
 * into :core:kyc-ui together; do not let them drift.
 */

/**
 * shared/kyc `normalizeASCII`: trims ASCII whitespace, drops every character in
 * [drop], upper-cases a–z, and REFUSES (null) any non-ASCII character rather
 * than letting an upper-case fold turn a look-alike into a valid identifier.
 */
internal fun normalizeAscii(input: String, drop: String = ""): String? {
    val trimmed = input.trimAsciiSpace()
    val out = StringBuilder(trimmed.length)
    for (c in trimmed) {
        if (c.code >= ASCII_LIMIT) return null
        if (drop.isNotEmpty() && drop.indexOf(c) >= 0) continue
        out.append(if (c in 'a'..'z') c - ('a' - 'A') else c)
    }
    return out.toString()
}

/** Trims exactly what Go's `trimSpaceASCII` trims: space, tab, LF, CR, VT, FF. */
internal fun String.trimAsciiSpace(): String {
    var start = 0
    var end = length
    while (start < end && this[start].isAsciiSpace()) start++
    while (end > start && this[end - 1].isAsciiSpace()) end--
    return substring(start, end)
}

private fun Char.isAsciiSpace(): Boolean =
    this == ' ' || this == '\t' || this == '\n' || this == '\r' || this == VERTICAL_TAB || this == FORM_FEED

/** shared/kyc `rtoStateCodes`: both spellings where a state changed code (OR/OD, UA/UK, TS/TG). */
internal val RTO_STATE_CODES: Set<String> = setOf(
    "AN", "AP", "AR", "AS", "BR", "CG",
    "CH", "DD", "DL", "DN", "GA", "GJ",
    "HP", "HR", "JH", "JK", "KA", "KL",
    "LA", "LD", "MH", "ML", "MN", "MP",
    "MZ", "NL", "OD", "OR", "PB", "PY",
    "RJ", "SK", "TN", "TR", "TS", "TG",
    "UK", "UA", "UP", "WB",
)

private val VERTICAL_TAB = Char(0x0B)
private val FORM_FEED = Char(0x0C)
private const val ASCII_LIMIT = 0x80
