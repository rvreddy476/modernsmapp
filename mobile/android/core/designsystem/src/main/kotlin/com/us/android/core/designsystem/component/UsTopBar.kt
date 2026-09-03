package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
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
 * Momentum's home top bar: the wordmark on the left, action slots on the
 * right (search, messages, and the bell with its [UsBadgedIcon] count).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsHomeTopBar(
    onHomeClick: () -> Unit = {},
    modifier: Modifier = Modifier,
    actions: @Composable RowScope.() -> Unit = {},
) {
    TopAppBar(
        // Momentum redesign: the brand row is the wordmark alone — no
        // logo chip. Still one tappable block, same scroll-to-top contract.
        title = {
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .clickable(onClick = onHomeClick)
                    .semantics { contentDescription = "Momentum home" },
            ) {
                UsWordmark(size = UsWordmarkSize.TopBar)
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
                        imageVector = UsIcons.Comment,
                        contentDescription = "Messages",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
                IconButton(onClick = {}) {
                    UsBadgedIcon(icon = UsIcons.Notifications, count = 3)
                }
            },
        )
    }
}

/**
 * A header glyph with Momentum's count badge: a 16dp WHITE disc on the
 * icon's top-right corner carrying the count at 9sp bold in the deep accent
 * red. Zero (or less) draws the bare icon. The badge is decorative to a
 * screen reader — put the count in the enclosing button's own description.
 */
@Composable
fun UsBadgedIcon(
    icon: ImageVector,
    count: Int,
    modifier: Modifier = Modifier,
    tint: Color = UsTheme.extended.textPrimary,
) {
    Box(modifier = modifier) {
        Icon(imageVector = icon, contentDescription = null, tint = tint)
        if (count > 0) {
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .offset(x = BADGE_OFFSET, y = -BADGE_OFFSET)
                    .size(BADGE_SIZE)
                    .background(Color.White, CircleShape),
            ) {
                Text(
                    text = if (count > BADGE_MAX) "$BADGE_MAX+" else "$count",
                    fontSize = BADGE_TEXT,
                    lineHeight = BADGE_TEXT,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.accentDeep,
                    maxLines = 1,
                )
            }
        }
    }
}

private val BADGE_SIZE = 16.dp
private val BADGE_OFFSET = 4.dp
private val BADGE_TEXT = 9.sp

/** Above this the exact number stops being useful and stops fitting. */
private const val BADGE_MAX = 99
