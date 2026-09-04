package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar

/**
 * The Friends feed: the home timeline narrowed to mutual follows
 * (`GET /v1/feed/home?circle_only=true`).
 *
 * The same posts and the same body as Home (founder, 2026-09-04) — no
 * "For You | Following | HashTag" row, because those are narrowings of the
 * whole timeline and this page is already one. It left the bottom bar for
 * the Explore launcher (founder, 2026-09-05), so it wears a titled bar with
 * a back arrow rather than the Momentum header: it is a page you open, and
 * [onBack] is the way out of it.
 */
@Composable
fun FriendsFeedScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    /** A video was tapped; :app switches to the Reels tab, which opens on it. */
    onOpenReels: () -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Friends) },
    ),
) {
    UsScaffold(
        topBar = { UsTopBar(title = "Friends", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        FeedContent(
            viewModel = viewModel,
            onOpenAuthor = onOpenAuthor,
            onOpenReels = onOpenReels,
            empty = FRIENDS_EMPTY,
            modifier = Modifier.padding(padding),
        )
    }
}

private val FRIENDS_EMPTY = FeedEmptyCopy(
    title = "No posts from friends yet",
    detail = "Posts from people who follow you back will show up here.",
)
