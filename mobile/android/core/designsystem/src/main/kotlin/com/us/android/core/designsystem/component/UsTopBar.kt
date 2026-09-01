package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The app's top bar.
 *
 * [onBack] is nullable and that is the whole design: a tab root has no back
 * affordance, a pushed screen does. Passing null renders no button rather than
 * a disabled one, because a control that does nothing is worse than its
 * absence.
 *
 * The title carries a `heading()` semantic so screen-reader users can jump to
 * it. Compose does not infer that from a Text inside a top bar.
 *
 * `ArrowBack` comes from `automirrored`, so the glyph flips in right-to-left
 * locales. The non-mirrored icon points the wrong way in RTL, which is the
 * kind of defect that ships because nobody tests in Arabic.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsTopBar(
    title: String,
    modifier: Modifier = Modifier,
    onBack: (() -> Unit)? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    CenterAlignedTopAppBar(
        title = {
            Text(
                text = title,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.semantics { heading() },
            )
        },
        navigationIcon = {
            if (onBack != null) {
                IconButton(onClick = onBack) {
                    Icon(
                        imageVector = UsIcons.Back,
                        contentDescription = "Back",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
            }
        },
        // Screen-level controls (author, overflow) live here as icons rather
        // than as buttons in the content, so they stay in one place across
        // every detail screen instead of moving with the layout.
        actions = actions,
        colors = TopAppBarDefaults.centerAlignedTopAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
        modifier = modifier,
    )
}

@Preview(name = "Top bar — tab root", showBackground = true)
@Composable
private fun UsTopBarRootPreview() {
    UsTheme { UsTopBar(title = "Home") }
}

@Preview(name = "Top bar — pushed screen", showBackground = true)
@Composable
private fun UsTopBarBackPreview() {
    UsTheme { UsTopBar(title = "Profile", onBack = {}) }
}

@Preview(name = "Top bar — long title truncates", showBackground = true)
@Composable
private fun UsTopBarLongTitlePreview() {
    UsTheme {
        UsTopBar(
            title = "A display name long enough to need truncating on a phone",
            onBack = {},
        )
    }
}

/**
 * The top bar for a tab root.
 *
 * Left-aligned and larger than [UsTopBar], which centres its title because a
 * detail screen has a back arrow on the left and needs the optical balance. A
 * tab root has no back arrow, so centring leaves a single word floating in the
 * middle of an empty bar — the thing that most makes a screen look like a
 * placeholder rather than a product.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsRootTopBar(
    title: String,
    modifier: Modifier = Modifier,
    actions: @Composable RowScope.() -> Unit = {},
) {
    TopAppBar(
        title = {
            Text(
                text = title,
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.semantics { heading() },
            )
        },
        actions = actions,
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
        modifier = modifier,
    )
}

/**
 * The modern top bar for the home feed screen.
 *
 * Features the Home / brand symbol on the top left, removes the bare "Home" text,
 * and provides action slots for search, new post creation, and direct messages on the right.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsHomeTopBar(
    onHomeClick: () -> Unit = {},
    modifier: Modifier = Modifier,
    actions: @Composable RowScope.() -> Unit = {},
) {
    TopAppBar(
        // Figma redesign (home 4:8): the brand row replaces the bare home
        // glyph — a white rounded-square "at" chip beside the ExtraBold
        // wordmark. Still one tappable block, same scroll-to-top contract.
        title = {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .clickable(onClick = onHomeClick)
                    .semantics { contentDescription = "atPost home" },
            ) {
                Box(
                    modifier = Modifier
                        .size(BRAND_CHIP)
                        .clip(RoundedCornerShape(UsTheme.radii.small))
                        .background(Color.White),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = "at",
                        style = MaterialTheme.typography.titleSmall,
                        fontWeight = FontWeight.ExtraBold,
                        color = Color.Black,
                    )
                }
                Text(
                    text = "atPost",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.ExtraBold,
                    color = UsTheme.extended.textPrimary,
                )
            }
        },
        actions = actions,
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
        modifier = modifier,
    )
}

/** Figma home top bar: the rounded-square "at" logo chip. */
private val BRAND_CHIP = 30.dp

@Preview(name = "Top bar — home feed", showBackground = true)
@Composable
private fun UsHomeTopBarPreview() {
    UsTheme {
        UsHomeTopBar(
            actions = {
                IconButton(onClick = {}) {
                    Icon(
                        imageVector = UsIcons.Explore,
                        contentDescription = "Search",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
                IconButton(onClick = {}) {
                    Icon(
                        imageVector = UsIcons.Create,
                        contentDescription = "New post",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
                IconButton(onClick = {}) {
                    Icon(
                        imageVector = UsIcons.Comment,
                        contentDescription = "Messages",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
            },
        )
    }
}
