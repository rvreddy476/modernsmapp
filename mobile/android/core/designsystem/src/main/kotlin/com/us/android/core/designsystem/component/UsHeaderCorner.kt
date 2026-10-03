// MatchingDeclarationName: the file is named for the corner it specifies; the enum is one glyph of it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.designsystem.component

import androidx.compose.ui.graphics.vector.ImageVector
import com.us.android.core.designsystem.icon.UsIcons

/**
 * One glyph of a section header's top-right corner.
 *
 * There is no "+" up here: Create lives in the bottom bar's centre button
 * and a second one in the header only duplicated it.
 */
enum class UsHeaderCornerAction(val description: String, val icon: ImageVector) {
    /** Opens the inbox. On Home's header only ([UsHomeHeaderCorner]). */
    MESSAGES("Messages", UsIcons.Comment),

    /** Opens the notification list. The bell, with the unread count as its badge. */
    NOTIFICATIONS("Notifications", UsIcons.Notifications),

    /** Opens the section's search. */
    SEARCH("Search", UsIcons.Search),

    /** Opens the section's own menu. Three dots, not the hamburger it was on Reels and Tube. */
    MORE("More", UsIcons.More),
}

/**
 * The corner of every header but Home's, in the order it is drawn from left
 * to right, so the LAST one sits at the corner.
 *
 * founder, 2026-10-02: Search, Notifications, More, with More at the corner
 * itself, on Reels, Tube, the Me tab and any page that grows a header. Every
 * header draws its corner by walking this list, with its own button idiom,
 * so they cannot drift apart again (they had: Reels and Tube put More before
 * Search, Tube had a "+", and only Home had the bell).
 */
val UsHeaderCorner: List<UsHeaderCornerAction> = listOf(
    UsHeaderCornerAction.SEARCH,
    UsHeaderCornerAction.NOTIFICATIONS,
    UsHeaderCornerAction.MORE,
)

/**
 * Home's corner, and Home's alone (founder, 2026-10-02: "keep exactly as it
 * is now"): Messages, the bell, Search, More. It is the one header with the
 * inbox on it; everywhere else the inbox is a row of a menu or a tab.
 */
val UsHomeHeaderCorner: List<UsHeaderCornerAction> = listOf(
    UsHeaderCornerAction.MESSAGES,
    UsHeaderCornerAction.NOTIFICATIONS,
    UsHeaderCornerAction.SEARCH,
    UsHeaderCornerAction.MORE,
)

/**
 * What the bell says to a screen reader. The count goes in the button's own
 * description — "Notifications" followed by a detached "3" is not a sentence
 * — and the badge itself is decorative. One wording for every header.
 */
fun usNotificationsDescription(unreadCount: Int): String = when {
    unreadCount <= 0 -> "Notifications"
    unreadCount == 1 -> "Notifications, 1 unread"
    else -> "Notifications, $unreadCount unread"
}
