package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar

/**
 * One tag's posts (`GET /v1/hashtags/{tag}/posts`), pushed from the HashTag
 * tab with the tag as its title and a back arrow.
 *
 * The rows come from post-service rather than feed-service and are hydrated
 * on the way in (see `HashtagPostHydrator`), so this is the same card list as
 * the feed — the reader cannot tell which service answered.
 */
@Composable
fun HashtagPostsScreen(
    tag: String,
    onBack: () -> Unit,
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Hashtag(tag)) },
    ),
) {
    val label = "#" + tag.removePrefix("#")
    UsScaffold(
        topBar = { UsTopBar(title = label, onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        FeedContent(
            viewModel = viewModel,
            onOpenPost = onOpenPost,
            onOpenAuthor = onOpenAuthor,
            empty = FeedEmptyCopy(
                title = "No posts with $label yet",
                detail = "Be the first to post with this tag.",
            ),
            modifier = Modifier.padding(padding),
        )
    }
}
