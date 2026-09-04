package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import com.us.android.core.designsystem.component.UsScaffold

/**
 * The Friends tab: the home timeline narrowed to mutual follows
 * (`GET /v1/feed/home?circle_only=true`).
 *
 * The same posts and the same body as Home under the same Momentum header
 * (founder, 2026-09-04) — no "For You | Following | HashTag" row, because
 * those are narrowings of the whole timeline and this tab is already one.
 * The header's search opens scoped to PEOPLE: on a page about who you know,
 * that is what a search is for.
 */
@Composable
fun FriendsFeedScreen(
    onOpenAuthor: (userId: String) -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    onOpenSearch: () -> Unit,
    /** A video was tapped; :app switches to the Reels tab, which opens on it. */
    onOpenReels: () -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Friends) },
    ),
) {
    UsScaffold(
        topBar = {
            MomentumHeader(
                onOpenSearch = onOpenSearch,
                onOpenMessages = onOpenMessages,
                onOpenNotifications = onOpenNotifications,
            )
        },
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
