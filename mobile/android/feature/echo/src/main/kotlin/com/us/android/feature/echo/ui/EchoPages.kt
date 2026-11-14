package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsEchoType
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.EchoProfile
import com.us.android.feature.echo.data.EchoProfileTab
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NoteSearchTab
import com.us.android.feature.echo.data.NoteStatus
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.joinedLabel
import com.us.android.feature.echo.data.parseInstant
import com.us.android.feature.echo.data.scheduleLabel
import java.time.Instant

/** Echo's profile page: cover, avatar, name and mark, @handle, joined, counts; Posts, Replies, Reposts, Media. */
@Composable
@Suppress("LongMethod") // the header and the four tabs in one list
fun EchoProfileScreen(destinations: EchoDestinations, viewModel: EchoProfileViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val feed by viewModel.feed(state.tab).collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val callbacks = rememberNoteCallbacks(
        actions = viewModel.actions,
        destinations = destinations,
        onReply = viewModel.gate::reply,
        onQuote = viewModel.gate::quote,
    )
    EchoLinks(destinations) {
        UsScaffold(
            applyPageGutter = false,
            topBar = { UsTopBar(title = state.profile?.name.orEmpty(), onBack = destinations.onBack) },
        ) { padding ->
            Box(modifier = Modifier.padding(padding).fillMaxSize()) {
                val profile = state.profile
                when {
                    profile == null && state.loading -> UsLoadingState()
                    profile == null -> UsErrorState(message = state.error.orEmpty(), onRetry = viewModel::load)
                    else -> EchoRefresh(refreshing = feed.refreshing, onRefresh = viewModel::refresh, top = 0.dp) {
                        NoteList(
                            feed = feed,
                            mediaUrls = urls,
                            callbacks = callbacks,
                            emptyTitle = profileEmpty(state.tab),
                            onRetry = viewModel::retry,
                            onLoadMore = viewModel::loadMore,
                            onSeen = viewModel.actions::seen,
                            header = {
                                item(key = "echo_profile_header") {
                                    ProfileHeader(
                                        profile = profile,
                                        urls = urls,
                                        own = viewModel.isOwn,
                                        followBusy = state.followBusy,
                                        onFollow = viewModel::toggleFollow,
                                        onEditProfile = { destinations.onOpenProfile(profile.userId) },
                                    )
                                }
                                item(key = "echo_profile_tabs") {
                                    TabStrip(
                                        labels = EchoProfileTab.entries.map { it.label },
                                        selected = state.tab.ordinal,
                                        tagPrefix = "echo_profile",
                                        onSelect = { viewModel.select(EchoProfileTab.entries[it]) },
                                    )
                                }
                            },
                        )
                    }
                }
                val message = remember(state.message) { state.message?.let { UsMessage(it) } }
                UsMessageHost(message = message, onDismiss = viewModel::messageShown)
            }
        }
    }
    NoteActionsHost(actions = viewModel.actions, destinations = destinations, onQuote = viewModel.gate::quote)
    ComposeGateHost(gate = viewModel.gate, destinations = destinations)
}

private fun profileEmpty(tab: EchoProfileTab): String = when (tab) {
    EchoProfileTab.POSTS -> "No posts yet"
    EchoProfileTab.REPLIES -> "No replies yet"
    EchoProfileTab.REPOSTS -> "No reposts yet"
    EchoProfileTab.MEDIA -> "No photos yet"
}

/**
 * The profile header as an Echo card (echo-look-v3): the cover as a rounded
 * banner of its own (never X's avatar cropped over it), then the avatar
 * beside the name in the serif, the mark, @handle, bio, joined, the counts
 * and Follow / Edit profile.
 */
@Composable
@Suppress("LongParameterList", "LongMethod") // cover, avatar, button, names, bio, joined, counts
private fun ProfileHeader(
    profile: EchoProfile,
    urls: Map<String, String>,
    own: Boolean,
    followBusy: Boolean,
    onFollow: () -> Unit,
    onEditProfile: () -> Unit,
) {
    EchoCard(
        modifier = Modifier
            .padding(start = UsTheme.spacing.l, end = UsTheme.spacing.l, top = UsTheme.spacing.m)
            .testTag("echo_profile_header"),
    ) {
        profile.coverMediaId?.let(urls::get)?.let { url ->
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(COVER_HEIGHT)
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(UsTheme.extended.fillSubtle),
            )
            Spacer(modifier = Modifier.height(UsTheme.spacing.l))
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            UsAvatar(
                name = profile.name,
                seed = profile.userId,
                imageUrl = profile.avatarMediaId?.let(urls::get),
                size = UsAvatarSize.Chat,
            )
            Column(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = profile.name,
                        style = UsEchoType.note,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.textPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    if (profile.mark != VerificationMark.NONE) VerificationTick(profile.mark)
                }
                Text(
                    text = profile.handle,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textTertiary
                )
            }
            val label = when {
                own -> "Edit profile"
                profile.viewerFollows -> "Following"
                else -> "Follow"
            }
            EchoPill(
                icon = null,
                label = label,
                description = label,
                lit = !own && !profile.viewerFollows,
                tag = "echo_profile_follow",
                onClick = if (followBusy) null else if (own) onEditProfile else onFollow,
            )
        }
        if (profile.bio.isNotBlank()) {
            Text(
                text = rememberEchoText(profile.bio),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.padding(top = UsTheme.spacing.l),
            )
        }
        joinedLabel(profile.joinedAt).takeIf { it.isNotBlank() }?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier.padding(top = UsTheme.spacing.m).testTag("echo_profile_joined"),
            )
        }
        Row(
            modifier = Modifier.padding(top = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
        ) {
            CountLabel(count = profile.following, label = "Following")
            CountLabel(count = profile.followers, label = if (profile.followers == 1L) "Follower" else "Followers")
        }
    }
}

/** A row of tabs inside a list (profile, search, hashtag): Echo's pill tabs. */
@Composable
internal fun TabStrip(
    labels: List<String>,
    selected: Int,
    tagPrefix: String,
    onSelect: (Int) -> Unit
) {
    PillTabs(
        labels = labels,
        selected = selected,
        tagPrefix = tagPrefix,
        onSelect = onSelect,
        modifier = Modifier.background(UsTheme.extended.bgCanvas).padding(vertical = UsTheme.spacing.s),
    )
}

/** A hashtag, search results (Top / Latest), Bookmarks or a post's Quotes. */
@Composable
fun EchoListScreen(destinations: EchoDestinations, viewModel: EchoListViewModel = hiltViewModel()) {
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val feed by viewModel.feed(tab).collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    val callbacks = rememberNoteCallbacks(
        actions = viewModel.actions,
        destinations = destinations,
        onReply = viewModel.gate::reply,
        onQuote = viewModel.gate::quote,
    )
    EchoLinks(destinations) {
        UsScaffold(
            applyPageGutter = false,
            topBar = { UsTopBar(title = listTitle(viewModel.kind, viewModel.arg), onBack = destinations.onBack) },
        ) { padding ->
            Column(modifier = Modifier.padding(padding).fillMaxSize()) {
                if (viewModel.kind.tabbed) {
                    TabStrip(
                        labels = NoteSearchTab.entries.map { it.label },
                        selected = tab.ordinal,
                        tagPrefix = "echo_list",
                        onSelect = { viewModel.select(NoteSearchTab.entries[it]) },
                    )
                }
                EchoRefresh(refreshing = feed.refreshing, onRefresh = viewModel::refresh, top = 0.dp) {
                    NoteList(
                        feed = feed,
                        mediaUrls = urls,
                        callbacks = callbacks,
                        emptyTitle = listEmptyTitle(viewModel.kind),
                        emptyDetail = listEmptyDetail(viewModel.kind),
                        onRetry = viewModel::retry,
                        onLoadMore = viewModel::loadMore,
                        onSeen = viewModel.actions::seen,
                    )
                }
            }
        }
    }
    NoteActionsHost(actions = viewModel.actions, destinations = destinations, onQuote = viewModel.gate::quote)
    ComposeGateHost(gate = viewModel.gate, destinations = destinations)
}

private fun listEmptyTitle(kind: EchoListKind): String = when (kind) {
    EchoListKind.HASHTAG, EchoListKind.SEARCH -> "No results"
    EchoListKind.BOOKMARKS -> "Save notes for later"
    EchoListKind.QUOTES -> "No quotes yet"
}

private fun listEmptyDetail(kind: EchoListKind): String? = when (kind) {
    EchoListKind.HASHTAG, EchoListKind.SEARCH -> "Try searching for something else."
    EchoListKind.BOOKMARKS -> "Save notes from their More menu to find them again here."
    EchoListKind.QUOTES -> null
}

/** The author's scheduled posts: when each goes out (or why it failed), change the time, delete. */
@Composable
@Suppress("LongMethod")
fun ScheduledScreen(destinations: EchoDestinations, viewModel: ScheduledViewModel = hiltViewModel()) {
    val feed by viewModel.feed.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    var editing by remember { mutableStateOf<Note?>(null) }
    var deleting by remember { mutableStateOf<Note?>(null) }
    UsScaffold(
        applyPageGutter = false,
        topBar = { UsTopBar(title = "Scheduled", onBack = destinations.onBack) }
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            EchoRefresh(refreshing = feed.refreshing, onRefresh = viewModel::refresh, top = 0.dp) {
                LazyColumn(
                    modifier = Modifier.fillMaxSize().testTag("echo_scheduled"),
                    contentPadding = PaddingValues(vertical = CARD_GAP),
                    verticalArrangement = Arrangement.spacedBy(CARD_GAP),
                ) {
                    when {
                        feed.loading && !feed.loaded -> item {
                            UsLoadingState(
                                modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)
                            )
                        }
                        feed.error != null -> item {
                            UsErrorState(
                                message = feed.error.orEmpty(),
                                onRetry = viewModel::retry,
                                modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT),
                            )
                        }
                        feed.empty -> item {
                            UsEmptyState(
                                title = "No scheduled notes",
                                detail = "Pick Later in the composer and the note shows up here.",
                                modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT),
                            )
                        }
                        else -> {
                            items(feed.items, key = { it.id }) { note ->
                                ScheduledRow(note = note, onEdit = { editing = note }, onDelete = { deleting = note })
                            }
                            item(key = "foot") { ListFoot(feed = feed, onRetry = viewModel::loadMore) }
                        }
                    }
                }
            }
            val shown = remember(message) { message?.let { UsMessage(it) } }
            UsMessageHost(message = shown, onDismiss = viewModel::messageShown)
        }
    }
    editing?.let { note ->
        SchedulePicker(
            initial = viewModel.scheduledAt(note),
            now = Instant.now(),
            onPicked = {
                editing = null
                viewModel.reschedule(note, it)
            },
            onDismiss = { editing = null },
        )
    }
    deleting?.let { note ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text("Delete scheduled post?") },
            text = { Text("It won't be posted, and this can't be undone.") },
            confirmButton = {
                TextButton(
                    onClick = {
                        deleting = null
                        viewModel.delete(note)
                    },
                    modifier = Modifier.testTag("echo_scheduled_delete_confirm"),
                ) { Text("Delete", color = UsTheme.extended.statusDanger) }
            },
            dismissButton = {
                TextButton(
                    onClick = { deleting = null }
                ) { Text("Cancel", color = UsTheme.extended.textSecondary) }
            },
        )
    }
}

@Composable
private fun ScheduledRow(note: Note, onEdit: () -> Unit, onDelete: () -> Unit) {
    val failed = note.status == NoteStatus.FAILED
    EchoCard(
        modifier = Modifier.padding(horizontal = UsTheme.spacing.l).testTag("echo_scheduled:${note.id}"),
        accent = !failed,
    ) {
        val at = parseInstant(note.scheduledAt)
        Text(
            text = when {
                failed -> "Not posted" + (note.publishError?.let { ": $it" } ?: "")
                at != null -> scheduleLabel(at)
                else -> "Scheduled"
            },
            style = MaterialTheme.typography.labelMedium,
            color = if (failed) UsTheme.extended.statusDanger else UsTheme.extended.echo.text,
            modifier = Modifier.testTag(if (failed) "echo_scheduled_failed" else "echo_scheduled_when"),
        )
        Text(
            text = note.text.ifBlank { if (note.poll != null) "Poll" else "Photo" },
            style = UsEchoType.note,
            color = UsTheme.extended.textPrimary,
            maxLines = PREVIEW_LINES,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(top = UsTheme.spacing.s),
        )
        Row(
            modifier = Modifier.padding(top = UsTheme.spacing.l),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            val change = if (failed) "Pick a new time" else "Change time"
            EchoPill(UsIcons.Clock, change, change, "echo_scheduled_edit", onClick = onEdit)
            EchoPill(UsIcons.Trash, "Delete", "Delete", "echo_scheduled_delete", onClick = onDelete)
        }
    }
}

private const val STATE_HEIGHT = 0.7f
private const val PREVIEW_LINES = 4
private val COVER_HEIGHT = 128.dp
