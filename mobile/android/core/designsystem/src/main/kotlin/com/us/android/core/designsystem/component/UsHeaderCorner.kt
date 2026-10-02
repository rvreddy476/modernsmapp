// MatchingDeclarationName: the file is named for the corner it specifies; the enum is one glyph of it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.designsystem.component

import androidx.compose.ui.graphics.vector.ImageVector
import com.us.android.core.designsystem.icon.UsIcons

/**
 * One glyph of a section header's top-right corner.
 *
 * founder, 2026-10-02: the corner of every section's top bar reads Search,
 * then the three-dots More, with More at the corner itself, the same on
 * Home, Reels and Tube. There is no "+" up here: Create lives in the bottom
 * bar's centre button and a second one in the header only duplicated it.
 */
enum class UsHeaderCornerAction(val description: String, val icon: ImageVector) {
    /** Opens the section's search. */
    SEARCH("Search", UsIcons.Search),

    /** Opens the section's own menu. Three dots, not the hamburger it was on Reels and Tube. */
    MORE("More", UsIcons.More),
}

/**
 * The corner, in the order it is drawn from left to right, so the LAST one
 * sits at the corner. Every section header draws its corner by walking this
 * list, with its own button idiom, so the three cannot drift apart again
 * (they had: Reels and Tube put More before Search, and Tube had a "+").
 */
val UsHeaderCorner: List<UsHeaderCornerAction> = listOf(UsHeaderCornerAction.SEARCH, UsHeaderCornerAction.MORE)
