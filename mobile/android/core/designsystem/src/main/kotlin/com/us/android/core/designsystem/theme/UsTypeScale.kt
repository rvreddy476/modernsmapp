package com.us.android.core.designsystem.theme

import androidx.compose.runtime.Immutable
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.sp

/**
 * The type scale — the ONE place a text size is written outside
 * [UsTypography] (founder's rule: colour and font come from one file; UI
 * audit 2026-10-21: 77 raw `.sp` across the feature modules, two of them
 * 9 sp). It mirrors the web's `--type-2xs … --type-4xl` tokens step for
 * step, so a label that is `text-xs` on the web is [xs] here.
 *
 * A screen reaches for [UsTypography]'s roles first (`bodyMedium`,
 * `labelSmall` …): they carry the family and the weight. A step from here
 * is for the places a role does not fit — a badge, a countdown, a canvas
 * the creator types on — and it is used as `fontSize = UsTypeScale.xs.size`
 * (with `lineHeight = …lineHeight` when the text wraps). Still `sp`, so
 * Android's font scaling applies.
 *
 * There is no step under 11 sp: `NoRawTypeSizeGuardTest` fails a `9.sp`
 * written anywhere else, and 11 is the floor on the web too.
 */
@Immutable
data class UsTypeStep(val size: TextUnit, val lineHeight: TextUnit)

object UsTypeScale {
    /** 11 / 14 — the web's `--type-2xs`: a pill, a rail label, a stamp. The floor. */
    val xs2 = UsTypeStep(size = 11.sp, lineHeight = 14.sp)

    /** 12 / 16 — `--type-xs`: meta, badges, a timestamp. */
    val xs = UsTypeStep(size = 12.sp, lineHeight = 16.sp)

    /** 14 / 20 — `--type-sm`: body in a dense list, chips, tab labels. */
    val sm = UsTypeStep(size = 14.sp, lineHeight = 20.sp)

    /** 16 / 24 — `--type-base`: body, menu rows, a field. */
    val base = UsTypeStep(size = 16.sp, lineHeight = 24.sp)

    /** 18 / 26 — `--type-lg`: a card title, a top bar title. */
    val lg = UsTypeStep(size = 18.sp, lineHeight = 26.sp)

    /** 20 / 28 — `--type-xl`: a sheet title. */
    val xl = UsTypeStep(size = 20.sp, lineHeight = 28.sp)

    /** 24 / 32 — `--type-2xl`: a page title, a composer title. */
    val xl2 = UsTypeStep(size = 24.sp, lineHeight = 32.sp)

    /** 30 / 36 — `--type-3xl`: a hero figure, large initials. */
    val xl3 = UsTypeStep(size = 30.sp, lineHeight = 36.sp)

    /** 36 / 40 — `--type-4xl`: a countdown, a percentage, a one-time code. */
    val xl4 = UsTypeStep(size = 36.sp, lineHeight = 40.sp)

    /** Every step, smallest first — what the guard test and a preview walk. */
    val steps: List<UsTypeStep> = listOf(xs2, xs, sm, base, lg, xl, xl2, xl3, xl4)
}
