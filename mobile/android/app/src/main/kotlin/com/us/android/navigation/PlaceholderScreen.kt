package com.us.android.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState

/**
 * A tab that exists in the shell but has no screen yet.
 *
 * Deliberately states WHY it is empty rather than showing a generic "coming
 * soon". Three of these four tabs are blocked on backend work, not on client
 * effort, and a placeholder that says so keeps the reason visible to whoever
 * opens the app next instead of burying it in a plan document.
 *
 * Each is replaced wholesale by its feature module; nothing here is intended
 * to survive.
 */
@Composable
fun PlaceholderScreen(
    title: String,
    reason: String,
    modifier: Modifier = Modifier,
    /** A back arrow when the placeholder is pushed rather than a tab root. */
    onBack: (() -> Unit)? = null,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = title, onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
            verticalArrangement = Arrangement.Center,
        ) {
            UsEmptyState(title = "$title isn't built yet", detail = reason)
            if (actionLabel != null && onAction != null) {
                UsSecondaryButton(
                    text = actionLabel,
                    onClick = onAction,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = UsTheme.spacing.pageHorizontal),
                )
            }
        }
    }
}

@Preview(name = "Placeholder tab", showBackground = true, heightDp = 400)
@Composable
private fun PlaceholderPreview() {
    UsTheme {
        PlaceholderScreen(
            title = "Reels",
            reason = "Blocked on HLS delivery: the master playlist exists in " +
                "storage but /v1/media/:id/serve returns 503, so there is no " +
                "playable URL yet.",
        )
    }
}
