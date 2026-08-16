package com.us.android.navigation

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.component.UsScaffold
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
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = title) },
        applyPageGutter = false,
    ) { padding ->
        UsEmptyState(
            title = "$title isn't built yet",
            detail = reason,
            modifier = Modifier.padding(padding),
        )
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
