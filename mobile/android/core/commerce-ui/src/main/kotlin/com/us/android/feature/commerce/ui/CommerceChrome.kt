package com.us.android.feature.commerce.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The two mini-apps' bars.
 *
 * MStore and MSeller are separate apps that happen to share a module, so they
 * share one bar shape rather than one bar: the wordmark on the left says which
 * of the two you are in, and the right-hand glyphs differ because the two
 * answer different questions.
 *
 * MStore's right side is, in order, favourites, the bag with its live count,
 * then the person. Orders and "Sell on MStore" used to hang off this bar as
 * two more glyphs; they moved into the profile menu, because five controls in
 * a header is a toolbar and the buyer only ever reaches for three of them.
 *
 * There is no "cart" anywhere in the product. The word, the glyph and the
 * route are all "bag"; only the server paths still say cart, because renaming
 * those is a migration, not a rename.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MStoreTopBar(
    bagCount: Int,
    person: CommercePerson,
    onOpenFavourites: () -> Unit,
    onOpenBag: () -> Unit,
    onOpenProfile: () -> Unit,
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
) {
    TopAppBar(
        title = { MStoreWordmark() },
        navigationIcon = {
            if (onBack != null) {
                CommerceGlyph(
                    icon = UsIcons.Back,
                    description = "Back",
                    onClick = onBack,
                    tag = "mstore_back",
                )
            }
        },
        actions = {
            CommerceGlyph(
                icon = UsIcons.HeartOutline,
                description = "Favourites",
                onClick = onOpenFavourites,
                tag = "mstore_favourites",
            )
            CommerceGlyph(
                icon = UsIcons.ShoppingBag,
                description = bagDescription(bagCount),
                onClick = onOpenBag,
                tag = "mstore_bag",
                count = bagCount,
            )
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .size(GLYPH_TARGET)
                    .pressScale(onOpenProfile)
                    .semantics { contentDescription = "Your MStore profile" }
                    .testTag("mstore_profile"),
            ) {
                UsAvatar(
                    name = person.name,
                    seed = person.seed,
                    imageUrl = person.avatarUrl,
                    size = UsAvatarSize.Small,
                )
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
        modifier = modifier.testTag("mstore_top_bar"),
    )
}

/**
 * MSeller's bar: the wordmark, then whatever the page needs on the right.
 *
 * No bag and no favourites — a seller's header answers "what is my shop
 * doing", and a buying control on it is a control for the other app.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MSellerTopBar(
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    TopAppBar(
        title = { MSellerWordmark() },
        navigationIcon = {
            if (onBack != null) {
                CommerceGlyph(
                    icon = UsIcons.Back,
                    description = "Back",
                    onClick = onBack,
                    tag = "mseller_back",
                )
            }
        },
        actions = actions,
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
        modifier = modifier.testTag("mseller_top_bar"),
    )
}

/**
 * A page inside MStore that is not the home: the wordmark still on the left,
 * a back glyph before it, and the page's own name on the line below.
 *
 * The founder's rule is that the wordmark sits at the left of the app bar on
 * EVERY screen of the mini-app, and a page five levels deep still needs its
 * own title. Stacking them is what satisfies both without a bar that reads
 * "MStore Delivery address".
 */
@Composable
fun MStorePageBar(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    actions: @Composable RowScope.() -> Unit = {},
) = CommercePageBar(
    wordmark = { MStoreWordmark() },
    title = title,
    onBack = onBack,
    backTag = "mstore_back",
    barTag = "mstore_page_bar",
    modifier = modifier,
    actions = actions,
)

/** The same, in MSeller's clothes. */
@Composable
fun MSellerPageBar(
    title: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    actions: @Composable RowScope.() -> Unit = {},
) = CommercePageBar(
    wordmark = { MSellerWordmark() },
    title = title,
    onBack = onBack,
    backTag = "mseller_back",
    barTag = "mseller_page_bar",
    modifier = modifier,
    actions = actions,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CommercePageBar(
    wordmark: @Composable () -> Unit,
    title: String,
    onBack: () -> Unit,
    backTag: String,
    barTag: String,
    modifier: Modifier = Modifier,
    actions: @Composable RowScope.() -> Unit = {},
) {
    Column(modifier = modifier.testTag(barTag)) {
        TopAppBar(
            title = wordmark,
            navigationIcon = {
                CommerceGlyph(
                    icon = UsIcons.Back,
                    description = "Back",
                    onClick = onBack,
                    tag = backTag,
                )
            },
            actions = actions,
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = Color.Transparent,
                scrolledContainerColor = Color.Transparent,
            ),
        )
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs)
                .semantics { heading() },
        )
    }
}

/**
 * Who is signed in, as the bar and the profile menu need them.
 *
 * [seed] is the user id so a person keeps one avatar colour across every
 * surface; [name] falls back to a neutral word rather than to an empty
 * circle while the profile read is in flight.
 */
data class CommercePerson(
    val name: String = "You",
    val seed: String = "",
    val avatarUrl: String? = null,
)

/**
 * One glyph on a commerce bar: a 44dp target, a Lucide stroke in the text
 * ramp, no ripple — the app's header gesture.
 *
 * [count] draws the header badge, the same white disc the notification bell
 * wears. The number goes in the button's own description rather than being
 * announced as a detached digit, so the badge itself stays decorative.
 */
@Composable
internal fun CommerceGlyph(
    icon: ImageVector,
    description: String,
    onClick: () -> Unit,
    tag: String,
    count: Int = 0,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(GLYPH_TARGET)
            .pressScale(onClick)
            .semantics { contentDescription = description }
            .testTag(tag),
    ) {
        if (count > 0) {
            UsBadgedIcon(icon = icon, count = count, tint = UsTheme.extended.textPrimary)
        } else {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier.size(GLYPH_SIZE),
            )
        }
    }
}

/**
 * MStore's search pill — Explore's field, in the shop.
 *
 * Not a text field: tapping it opens the search results page, which owns the
 * keyboard and the query. A pill that both looks like a button and edits in
 * place is the control people tap twice.
 */
@Composable
fun MStoreSearchPill(
    query: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(PILL_RADIUS)
    val showing = query.takeIf { it.isNotBlank() }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = showing?.let { "Search MStore, showing $it" } ?: "Search MStore"
            }
            .padding(horizontal = PILL_PADDING_H, vertical = PILL_PADDING_V)
            .testTag("mstore_search"),
    ) {
        Icon(
            imageVector = UsIcons.Search,
            contentDescription = null,
            tint = UsTheme.extended.textDim,
            modifier = Modifier.size(PILL_GLYPH),
        )
        Text(
            text = showing ?: "Search for products, brands and more",
            style = MaterialTheme.typography.bodyLarge,
            color = if (showing != null) UsTheme.extended.textPrimary else UsTheme.extended.textDim,
            maxLines = 1,
        )
    }
}

/** "Bag, empty" / "Bag, 3 items" — the count read as a sentence, not a digit. */
internal fun bagDescription(count: Int): String = when {
    count <= 0 -> "Bag, empty"
    count == 1 -> "Bag, 1 item"
    else -> "Bag, $count items"
}

private val GLYPH_TARGET = 44.dp

/** 24dp, the same as UsBadgedIcon draws — so the bag does not resize when its badge appears. */
private val GLYPH_SIZE = 24.dp
private val HAIRLINE = 1.dp
private val PILL_RADIUS = 22.dp
private val PILL_PADDING_H = 16.dp
private val PILL_PADDING_V = 12.dp
private val PILL_GLYPH = 20.dp
