package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.feed.ui.hashtag.HashtagFollowViewModel

/**
 * One tag's posts (`GET /v1/hashtags/{tag}/posts`), pushed from a tapped
 * #tag with the tag as its title and a back arrow.
 *
 * The rows come from post-service rather than feed-service and are hydrated
 * on the way in (see `HashtagPostHydrator`), so this is the same card list as
 * the feed — the reader cannot tell which service answered. The body is
 * Home's full-screen pager (2026-10-18), below the titled bar.
 *
 * Follow / Following sits at the bar's end (followed hashtags, 2026-10-24):
 * the tag's posts then come to the Hashtags feed. No button until the state
 * is read, so it never says Follow over a tag already followed.
 */
@Composable
fun HashtagPostsScreen(
    tag: String,
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Hashtag(tag)) },
    ),
    follow: HashtagFollowViewModel = hiltViewModel(),
) {
    val label = "#" + tag.removePrefix("#")
    LaunchedEffect(tag) { follow.bind(tag) }
    val followState by follow.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = {
            UsTopBar(
                title = label,
                onBack = onBack,
                actions = {
                    followState.following?.let { following ->
                        UsPillButton(
                            text = followState.buttonLabel,
                            onClick = follow::toggle,
                            filled = !following,
                            busy = followState.busy,
                            modifier = Modifier
                                .padding(end = UsTheme.spacing.m)
                                .semantics {
                                    role = Role.Switch
                                    stateDescription = if (following) "Following $label" else "Not following $label"
                                }
                                .testTag("hashtag_follow"),
                        )
                    }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            FeedContent(
                viewModel = viewModel,
                onOpenAuthor = onOpenAuthor,
                empty = FeedEmptyCopy(
                    title = "No posts with $label yet",
                    detail = "Be the first to post with this tag.",
                ),
            )
            UsMessageHost(message = followState.message, onDismiss = follow::dismissMessage)
        }
    }
}
