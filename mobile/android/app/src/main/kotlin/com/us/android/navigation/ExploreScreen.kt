package com.us.android.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The Explore tab, and the way into commerce.
 *
 * ## Why the shop lives here
 *
 * The commerce graph was fully registered and completely unreachable: the
 * bottom bar has five tabs and none of them is a shop, and nothing anywhere
 * called `navigateToCommerce()`. Eight screens, a working checkout and a
 * payment handoff that no user could get to.
 *
 * Explore is the natural host. It was a placeholder — "search is not built
 * yet" — so putting the shop here costs nothing that existed, and it needs no
 * change to [TopLevelDestination] or to `:core:designsystem`'s tab list. A
 * sixth tab would have touched the design system, the tab enum and the test
 * that asserts the two stay in step, for a navigation decision that is really
 * a product one.
 *
 * The design-gallery link stays: it is how the tokens get reviewed on a real
 * device, and removing it would take away a working tool to make room for a
 * new one.
 */
@Composable
fun ExploreScreen(
    onOpenShop: () -> Unit,
    onOpenOrders: () -> Unit,
    onOpenSellerHub: () -> Unit,
    onOpenGallery: () -> Unit,
    modifier: Modifier = Modifier,
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = "Explore") },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            UsButton(
                text = "Shop",
                onClick = onOpenShop,
                modifier = Modifier.fillMaxWidth(),
            )
            UsSecondaryButton(
                text = "My orders",
                onClick = onOpenOrders,
                modifier = Modifier.fillMaxWidth(),
            )
            // The seller half. Reachable by everyone: the hub itself answers
            // "you do not have a shop yet" for a buyer, which is a better
            // answer than a button that is not there.
            UsSecondaryButton(
                text = "My shop",
                onClick = onOpenSellerHub,
                modifier = Modifier.fillMaxWidth(),
            )
            UsSecondaryButton(
                text = "Open the design gallery",
                onClick = onOpenGallery,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Preview(name = "Explore", showBackground = true, heightDp = 400)
@Composable
private fun ExplorePreview() {
    UsTheme {
        ExploreScreen(
            onOpenShop = {},
            onOpenOrders = {},
            onOpenSellerHub = {},
            onOpenGallery = {},
        )
    }
}
