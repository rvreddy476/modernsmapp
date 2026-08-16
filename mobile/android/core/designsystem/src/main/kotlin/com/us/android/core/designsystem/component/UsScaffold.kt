package com.us.android.core.designsystem.component

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
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
    ) { inner ->
        val gutter = if (applyPageGutter) UsTheme.spacing.pageHorizontal else 0.dp
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(inner)
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
                text = "US",
                style = MaterialTheme.typography.headlineMedium,
                color = UsTheme.extended.textPrimary,
            )
        }
    }
}
