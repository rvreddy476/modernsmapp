// MatchingDeclarationName: this file's primary export is the UsNavigationBar
// composable; UsNavItem is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.designsystem.component

import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * One destination in the bottom bar.
 *
 * [contentDescription] is separate from [label] because the two are not always
 * the same sentence: "Me" is a fine visual label and a poor spoken one.
 */
@Immutable
data class UsNavItem(
    val label: String,
    val icon: ImageVector,
    val contentDescription: String = label,
)

/**
 * The app's bottom navigation bar.
 *
 * Deliberately dumb: it receives the item list, the selected index and a
 * callback. It knows nothing about routes, the back stack or which feature
 * owns a tab. That is what lets the shell own navigation policy in one place
 * and keeps this component previewable and screenshot-testable.
 *
 * Selection is passed as an index rather than a route so the design system
 * never gains a dependency on the navigation library.
 */
@Composable
fun UsNavigationBar(
    items: List<UsNavItem>,
    selectedIndex: Int,
    onSelect: (index: Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    NavigationBar(
        modifier = modifier,
        containerColor = UsTheme.extended.bgCard,
        tonalElevation = 0.dp,
    ) {
        items.forEachIndexed { index, item ->
            val selected = index == selectedIndex
            NavigationBarItem(
                selected = selected,
                onClick = { onSelect(index) },
                icon = {
                    Icon(
                        imageVector = item.icon,
                        // The visible label is gone, so the icon now carries
                        // the accessible name. Leaving this null would make the
                        // whole bar five unlabelled buttons.
                        contentDescription = item.contentDescription,
                    )
                },
                // No labels. Five words across the bottom of every screen buy
                // nothing after the first session — these five destinations are
                // the most-used surfaces in the app and are learned immediately
                // — and they cost vertical space on every screen forever.
                // The names survive in [UsNavItem.label] for accessibility.
                label = null,
                colors = NavigationBarItemDefaults.colors(
                    selectedIconColor = UsTheme.extended.textPrimary,
                    selectedTextColor = UsTheme.extended.textPrimary,
                    unselectedIconColor = UsTheme.extended.textMuted,
                    unselectedTextColor = UsTheme.extended.textMuted,
                    indicatorColor = UsTheme.extended.bgCardHover,
                ),
            )
        }
    }
}

/**
 * The product's five top-level destinations, in order.
 *
 * Lives here rather than in `:app` so the preview below renders the real bar
 * rather than a sample, and so the labels have one definition. The mapping
 * from an index to a route stays in the shell — this list is presentation.
 *
 * Ported from the Flutter shell's tab order (see the migration plan §1) so
 * muscle memory carries over for anyone moving between the two builds.
 */
val UsDefaultNavItems: List<UsNavItem> = listOf(
    UsNavItem("Home", UsIcons.Home),
    UsNavItem("Friends", UsIcons.Friends),
    UsNavItem("Reels", UsIcons.Reels),
    UsNavItem("Explore", UsIcons.Explore),
    UsNavItem("Me", UsIcons.Profile, contentDescription = "My profile"),
)

@Preview(name = "Navigation bar", showBackground = true)
@Composable
private fun UsNavigationBarPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems, selectedIndex = 0, onSelect = {})
    }
}

@Preview(name = "Navigation bar — last tab", showBackground = true)
@Composable
private fun UsNavigationBarLastPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems, selectedIndex = 4, onSelect = {})
    }
}
