package com.us.android.designreview

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.component.UsDefaultNavItems
import com.us.android.core.designsystem.component.UsNavigationBar
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.explore.ExploreContent
import com.us.android.explore.launcherTiles

/** Debug-only, offline visual fixture. Never part of a release or an auth bypass. */
@Preview(showBackground = true, widthDp = 360, heightDp = 800)
@Composable
fun MobileDesignReview() {
    var query by rememberSaveable { mutableStateOf("") }
    var tab by rememberSaveable { mutableIntStateOf(EXPLORE_TAB_INDEX) }
    UsTheme {
        UsScaffold(
            topBar = { UsTopBar("Explore") },
            bottomBar = { UsNavigationBar(UsDefaultNavItems, tab, { tab = it }, centerAction = {}) },
            applyPageGutter = false,
        ) { padding ->
            Box(Modifier.fillMaxSize().padding(padding)) {
                ExploreContent(launcherTiles(), SAMPLE_UNREAD, query, { query = it }, {}, {})
            }
        }
    }
}

private const val EXPLORE_TAB_INDEX = 3
private const val SAMPLE_UNREAD = 3
