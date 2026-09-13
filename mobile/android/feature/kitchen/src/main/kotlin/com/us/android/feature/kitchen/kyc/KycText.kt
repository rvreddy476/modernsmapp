package com.us.android.feature.kitchen.kyc

/*
 * Client-side format checks for the identifiers restaurant onboarding captures.
 *
 * Each validator MIRRORS Architecture/shared/kyc (Go), rule for rule, and its
 * tests carry the Go tests' vectors. They are a courtesy, not a gate: the
 * server re-validates everything, and these exist so a typo is caught before a
 * round trip rather than after it.
 *
 * LIFTABLE: this package has no Android, Compose or kitchen dependency on
 * purpose. When Feast Rider needs the same checks, move it into :core:kyc-ui
 * unchanged (with its tests) rather than copying it.
 */

/**
 * shared/kyc `normalizeASCII`: trims ASCII whitespace, drops every character in
 * [drop], upper-cases a–z, and REFUSES (null) any non-ASCII character — rather
 * than letting an upper-case fold turn a look-alike such as U+017F (long s) or
 * U+0131 (dotless i) into a valid ASCII identifier.
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

private val VERTICAL_TAB = Char(0x0B)
private val FORM_FEED = Char(0x0C)
private const val ASCII_LIMIT = 0x80
