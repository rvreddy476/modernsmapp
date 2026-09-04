package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import com.us.android.core.designsystem.component.UsRootTopBar
import com.us.android.core.designsystem.component.UsScaffold

/**
 * The Friends tab: the home timeline narrowed to mutual follows
 * (`GET /v1/feed/home?circle_only=true`).
 *
 * The same cards and the same body as Home under a root title bar — no
 * "For You | Following | HashTag" row, because those are narrowings of the
 * whole timeline and this tab is already one.
 */
@Composable
fun FriendsFeedScreen(
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Friends) },
    ),
) {
    UsScaffold(
        topBar = { UsRootTopBar(title = "Friends") },
        applyPageGutter = false,
    ) { padding ->
        FeedContent(
            viewModel = viewModel,
            onOpenAuthor = onOpenAuthor,
            empty = FRIENDS_EMPTY,
            modifier = Modifier.padding(padding),
        )
    }
}

private val FRIENDS_EMPTY = FeedEmptyCopy(
    title = "No posts from friends yet",
    detail = "Posts from people who follow you back will show up here.",
)
