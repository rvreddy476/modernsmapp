package com.us.android.core.designsystem.component

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.union
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.ScaffoldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * App-standard screen container.
 *
 * Exists so screens do not each re-derive the background colour and page
 * gutter. Content receives the combined insets so a screen never has to
 * reason about system bars.
 */
@Composable
fun UsScaffold(
    modifier: Modifier = Modifier,
    topBar: @Composable () -> Unit = {},
    bottomBar: @Composable () -> Unit = {},
    applyPageGutter: Boolean = true,
    content: @Composable (PaddingValues) -> Unit,
) {
    Scaffold(
        modifier = modifier.fillMaxSize(),
        topBar = topBar,
        bottomBar = bottomBar,
        containerColor = MaterialTheme.colorScheme.background,
        contentColor = UsTheme.extended.textPrimary,
        // The KEYBOARD is an inset like any other.
        //
        // MainActivity calls enableEdgeToEdge(), so the window no longer
        // resizes for the IME and `adjustResize` in the manifest does
        // nothing. Folding the ime inset in here — rather than asking every
        // screen with a text field to remember imePadding() — is what stops
        // a composer or a submit button from sitting behind the keyboard
        // being used to type into it.
        //
        // union() takes the LARGER of the two per side, so the bottom is
        // max(navigation bar, keyboard) and the navigation bar is never
        // counted twice while the keyboard is up.
        //
        // Screens that discard the PaddingValues this hands them are not
        // covered and apply imePadding() themselves; the three auth screens
        // are the only ones.
        contentWindowInsets = ScaffoldDefaults.contentWindowInsets.union(WindowInsets.ime),
    ) { inner ->
        val gutter = if (applyPageGutter) UsTheme.spacing.pageHorizontal else 0.dp
        // The inset is HANDED to content, not applied here.
        //
        // Applying it and passing it was a real defect: every caller already
        // does `Modifier.padding(padding)`, so the top bar's height was
        // reserved twice and the feed began a full bar-height below where it
        // should. It read as a mysterious empty band under the title.
        //
        // Consuming it in the content is also what lets a list scroll its
        // items UNDER the bars while keeping the first and last item clear of
        // them — padding the container instead clips the scroll to a letterbox.
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = gutter),
        ) {
            content(inner)
        }
    }
}

@Preview(name = "Scaffold", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsScaffoldPreview() {
    UsTheme {
        UsScaffold {
            Text(
                text = "Momentum",
                style = MaterialTheme.typography.headlineMedium,
                color = UsTheme.extended.textPrimary,
            )
        }
    }
}
