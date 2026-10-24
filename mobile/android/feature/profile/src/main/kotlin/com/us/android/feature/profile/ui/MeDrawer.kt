// MatchingDeclarationName: the file is the drawer; the enums and the function are its rows.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.profile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsEndDrawer
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The groups of the Me tab's ☰ drawer (founder, 2026-10-17), in the order
 * they are drawn. [SETTINGS] has no heading: it is the one row under the
 * divider, the door to the whole Settings & privacy page.
 */
enum class MeDrawerGroup(val heading: String?) {
    ACTIVITY("Your activity"),
    CREATOR_TOOLS("Creator tools"),
    SETTINGS(null),
}

/**
 * The rows of the Me tab's drawer, in the order they are drawn. It replaced
 * the ⋮ More sheet (Creator Hub, Messages, Settings) on 2026-10-17: a short
 * list of the viewer's own things, then one row to Settings & privacy. Every
 * row is a screen that already exists.
 */
enum class MeDrawerRow(val label: String, val icon: ImageVector, val group: MeDrawerGroup) {
    /** The own profile's Liked grid tab. */
    LIKED("Liked", UsIcons.HeartOutline, MeDrawerGroup.ACTIVITY),

    /** The own profile's Saved grid tab. */
    SAVED("Saved", UsIcons.BookmarkOutline, MeDrawerGroup.ACTIVITY),

    /** The tags the viewer follows (followed hashtags, 2026-10-24), the feed's graph. */
    FOLLOWED_HASHTAGS("Followed hashtags", UsIcons.Hash, MeDrawerGroup.ACTIVITY),

    /** The Offline page (Tube's graph, the same one Reels' header opens). */
    OFFLINE("Offline videos", UsIcons.ArrowDownToLine, MeDrawerGroup.ACTIVITY),

    /** The viewer's pending credit requests (video credits, 2026-10-23), Tube's graph. */
    CREDIT_REQUESTS("Credit requests", UsIcons.Clapperboard, MeDrawerGroup.ACTIVITY),

    /** The memberships the viewer pays for (paid memberships, 2026-10-23): renew, change, cancel. */
    MY_MEMBERSHIPS("My memberships", UsIcons.Star, MeDrawerGroup.ACTIVITY),

    /** The chat home. */
    MESSAGES("Messages", UsIcons.Comment, MeDrawerGroup.ACTIVITY),

    /** The read-only Creator Hub (T-31). */
    CREATOR_HUB("Creator Hub", UsIcons.Gauge, MeDrawerGroup.CREATOR_TOOLS),

    /** The creator's Memberships page (2026-10-23): eligibility, levels, members. */
    MEMBERSHIPS("Memberships", UsIcons.Award, MeDrawerGroup.CREATOR_TOOLS),

    /** The Create sheet, the same one the bar's centre button opens. */
    UPLOAD("Upload", UsIcons.Upload, MeDrawerGroup.CREATOR_TOOLS),

    /** The Settings & privacy page. */
    SETTINGS("Settings & privacy", UsIcons.Settings, MeDrawerGroup.SETTINGS),
}

/** One heading's rows, as drawn. A group with nothing to offer is left out. */
data class MeDrawerSection(val group: MeDrawerGroup, val rows: List<MeDrawerRow>)

/**
 * Where the Me tab can send each row. A false door hides its row: a row
 * that does nothing is never drawn.
 */
data class MeDrawerDoors(
    /** The own profile has loaded with its grid, so the Liked and Saved tabs are there to open. */
    val grid: Boolean = false,
    /** Followed hashtags (2026-10-24). */
    val followedHashtags: Boolean = false,
    val offline: Boolean = false,
    val creditRequests: Boolean = false,
    val myMemberships: Boolean = false,
    val messages: Boolean = false,
    val creatorHub: Boolean = false,
    val memberships: Boolean = false,
    val upload: Boolean = false,
    val settings: Boolean = false,
)

/**
 * The drawer, grouped. Nothing at all on someone else's profile — theirs
 * keeps its own ⋮ menu ([profileMenuRows]). Liked and Saved follow the
 * grid's own rule for which tabs the viewer gets ([gridTabsFor]). Pure, so
 * order and visibility are a table test.
 */
fun meDrawerSections(isOwnProfile: Boolean, doors: MeDrawerDoors): List<MeDrawerSection> {
    if (!isOwnProfile) return emptyList()
    val ownTabs = gridTabsFor(isOwn = true)
    val offered = MeDrawerRow.entries.filter { row ->
        when (row) {
            MeDrawerRow.LIKED -> doors.grid && ProfileGridTab.LIKED in ownTabs
            MeDrawerRow.SAVED -> doors.grid && ProfileGridTab.SAVED in ownTabs
            MeDrawerRow.FOLLOWED_HASHTAGS -> doors.followedHashtags
            MeDrawerRow.OFFLINE -> doors.offline
            MeDrawerRow.CREDIT_REQUESTS -> doors.creditRequests
            MeDrawerRow.MY_MEMBERSHIPS -> doors.myMemberships
            MeDrawerRow.MESSAGES -> doors.messages
            MeDrawerRow.CREATOR_HUB -> doors.creatorHub
            MeDrawerRow.MEMBERSHIPS -> doors.memberships
            MeDrawerRow.UPLOAD -> doors.upload
            MeDrawerRow.SETTINGS -> doors.settings
        }
    }
    return MeDrawerGroup.entries.mapNotNull { group ->
        offered.filter { it.group == group }.takeIf { it.isNotEmpty() }?.let { MeDrawerSection(group, it) }
    }
}

/** The grid tab a row opens on the own profile, or null for a row that leaves it. */
fun MeDrawerRow.gridTab(): ProfileGridTab? = when (this) {
    MeDrawerRow.LIKED -> ProfileGridTab.LIKED
    MeDrawerRow.SAVED -> ProfileGridTab.SAVED
    else -> null
}

/**
 * The Me tab's ☰ drawer: [UsEndDrawer] from the right, grey headings over
 * each group, rows of a glyph in a soft square, the label and a chevron. A
 * row slides the drawer away FIRST and then acts.
 */
@Composable
internal fun MeDrawer(sections: List<MeDrawerSection>, onRow: (MeDrawerRow) -> Unit, onDismiss: () -> Unit) {
    UsEndDrawer(onDismiss = onDismiss, modifier = Modifier.testTag("me_drawer")) { close ->
        Spacer(Modifier.height(UsTheme.spacing.xl))
        sections.forEach { section ->
            val title = section.group.heading
            if (title == null) {
                HorizontalDivider(
                    color = UsTheme.extended.borderSubtle,
                    modifier = Modifier.padding(vertical = UsTheme.spacing.m),
                )
            } else {
                Text(
                    text = title,
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier
                        .padding(horizontal = UsTheme.spacing.xxxxl, vertical = UsTheme.spacing.m)
                        .semantics { heading() },
                )
            }
            section.rows.forEach { row ->
                MeDrawerRowItem(row = row, onClick = { close { onRow(row) } })
            }
        }
        Spacer(Modifier.height(UsTheme.spacing.xl))
    }
}

@Composable
private fun MeDrawerRowItem(row: MeDrawerRow, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .clickable(role = Role.Button, onClickLabel = row.label, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xxxxl)
            .testTag("me_drawer_row:${row.name.lowercase()}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .size(ICON_SQUARE)
                .background(UsTheme.extended.fillSubtle, RoundedCornerShape(UsTheme.radii.medium)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = row.icon,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier.size(ROW_GLYPH),
            )
        }
        Text(
            text = row.label,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Icon(
            imageVector = UsIcons.ChevronRight,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(CHEVRON),
        )
    }
}

private val ROW_HEIGHT = 52.dp
private val ICON_SQUARE = 36.dp
private val ROW_GLYPH = 20.dp
private val CHEVRON = 18.dp
