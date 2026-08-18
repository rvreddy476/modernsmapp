package com.us.android.core.designsystem.component

import androidx.compose.foundation.layout.RowScope
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
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
