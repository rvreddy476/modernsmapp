package com.us.android.feature.echo.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.ProfileNotesTab
import java.time.Instant

/**
 * A profile's Echo tab: Notes, Replies, Reposts. Drawn inside the profile's
 * own scrolling page, so it is a plain column with "Show more" rather than a
 * second lazy list. `:app` hands it to the profile only while Echo is open.
 */
@Composable
fun EchoProfileNotes(
    userId: String,
    onOpenNote: (noteId: String) -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    refreshSeq: Int = 0,
    viewModel: ProfileNotesViewModel = hiltViewModel(key = "echo-profile:$userId"),
) {
    LaunchedEffect(userId) { viewModel.start(userId) }
    LaunchedEffect(refreshSeq) { if (refreshSeq > 0) viewModel.refresh() }
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val feed by viewModel.feed(tab).collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val share = rememberNoteSharer()
    val now = remember(feed.items) { Instant.now() }
    val callbacks = NoteCallbacks(
        onOpen = { onOpenNote(it.id) },
        onOpenAuthor = onOpenProfile,
        onReply = { onOpenNote(it.id) },
        onRepost = viewModel::toggleRepost,
        // Quoting needs eligibility and the composer: on the note's own page.
        onQuote = { onOpenNote(it.id) },
        onLike = viewModel::toggleLike,
        onShare = share,
    )

    Column(modifier = Modifier.fillMaxWidth().testTag("echo_profile_tab")) {
        Row(
            modifier = Modifier.padding(vertical = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            ProfileNotesTab.entries.forEach { t ->
                FilterChip(
                    selected = t == tab,
                    onClick = { viewModel.select(t) },
                    label = { Text(t.label) },
                    colors = FilterChipDefaults.filterChipColors(
                        selectedContainerColor = UsTheme.extended.accent,
                        selectedLabelColor = UsTheme.extended.onAccent,
                        labelColor = UsTheme.extended.textSecondary,
                    ),
                    modifier = Modifier.testTag("echo_profile_tab:${t.wire}"),
                )
            }
        }
        when {
            feed.loading && !feed.loaded -> UsLoadingState(modifier = Modifier.fillMaxWidth())
            feed.error != null -> UsErrorState(message = feed.error.orEmpty(), onRetry = viewModel::retry)
            feed.empty -> Text(
                text = emptyText(tab),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier.padding(vertical = UsTheme.spacing.xxl),
            )
            else -> {
                feed.items.forEach { note -> NoteCard(note = note, mediaUrls = urls, callbacks = callbacks, now = now) }
                if (feed.canLoadMore || feed.loadingMore || feed.moreError != null) {
                    if (feed.moreError == null && !feed.loadingMore) {
                        androidx.compose.material3.TextButton(
                            onClick = viewModel::loadMore,
                            modifier = Modifier.testTag("echo_profile_more")
                        ) {
                            Text("Show more", color = UsTheme.extended.accent)
                        }
                    } else {
                        ListFoot(feed = feed, onRetry = viewModel::loadMore)
                    }
                }
            }
        }
    }
}

internal fun emptyText(tab: ProfileNotesTab): String = when (tab) {
    ProfileNotesTab.NOTES -> "No notes yet"
    ProfileNotesTab.REPLIES -> "No replies yet"
    ProfileNotesTab.REPOSTS -> "No reposts yet"
}
