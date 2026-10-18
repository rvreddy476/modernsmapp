package com.us.android.feature.post.drafts

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import coil3.compose.AsyncImage
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.post.data.PostDraft
import com.us.android.feature.post.data.PostDraftsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The Drafts list as the screen draws it. */
sealed interface DraftsUiState {
    data object Loading : DraftsUiState
    data class Content(val drafts: List<PostDraft>, val thumbs: Map<String, String?> = emptyMap()) : DraftsUiState
    data object Failed : DraftsUiState
}

/**
 * The author's saved photo and text drafts (2026-10-18), from the details
 * screen's Drafts button and the Create sheet. Each row's first photo is
 * resolved once, as the list loads.
 */
@HiltViewModel
class DraftsViewModel @Inject constructor(
    private val drafts: PostDraftsRepository,
    private val media: MediaRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<DraftsUiState>(DraftsUiState.Loading)
    val state: StateFlow<DraftsUiState> = _state.asStateFlow()

    /** A draft could not be deleted; said once. */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    fun load() {
        viewModelScope.launch {
            _state.value = when (val result = drafts.list()) {
                is AppResult.Success -> DraftsUiState.Content(result.data)
                is AppResult.Failure -> DraftsUiState.Failed
            }
            (state.value as? DraftsUiState.Content)?.drafts?.forEach { draft ->
                val first = draft.mediaIds.firstOrNull() ?: return@forEach
                launch {
                    val url = (media.delivery(first) as? AppResult.Success)?.data?.posterUrl
                    _state.update { current ->
                        (current as? DraftsUiState.Content)?.let { it.copy(thumbs = it.thumbs + (draft.id to url)) } ?: current
                    }
                }
            }
        }
    }

    fun delete(draftId: String) {
        viewModelScope.launch {
            when (drafts.delete(draftId)) {
                is AppResult.Success -> _state.update { current ->
                    (current as? DraftsUiState.Content)?.let { it.copy(drafts = it.drafts.filterNot { d -> d.id == draftId }) } ?: current
                }
                is AppResult.Failure -> _notice.value = "That draft couldn't be deleted."
            }
        }
    }

    fun onNoticeShown() {
        _notice.value = null
    }
}

@Composable
fun DraftsScreen(
    onBack: () -> Unit,
    onOpenDraft: (draftId: String) -> Unit,
    viewModel: DraftsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val notice by viewModel.notice.collectAsStateWithLifecycle()
    var deleting by remember { mutableStateOf<PostDraft?>(null) }
    // Fresh every time the list is shown: a draft edited or published from
    // the editor must not linger here.
    LaunchedEffect(Unit) { viewModel.load() }
    UsScaffold(
        topBar = { UsTopBar(title = "Drafts", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize().testTag("post-drafts")) {
            when (val current = state) {
                DraftsUiState.Loading -> UsLoadingState(label = "Loading drafts")
                DraftsUiState.Failed -> UsErrorState(message = "We couldn't load your drafts.", onRetry = viewModel::load)
                is DraftsUiState.Content -> if (current.drafts.isEmpty()) {
                    UsEmptyState(title = "No drafts", detail = "Posts you save for later show up here.")
                } else {
                    LazyColumn(modifier = Modifier.fillMaxSize()) {
                        items(current.drafts, key = { it.id }) { draft ->
                            DraftRow(
                                draft = draft,
                                thumb = current.thumbs[draft.id],
                                onOpen = { onOpenDraft(draft.id) },
                                onDelete = { deleting = draft },
                            )
                        }
                    }
                }
            }
            notice?.let { message ->
                Text(
                    text = message,
                    color = UsTheme.extended.statusDanger,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.align(Alignment.BottomCenter).padding(UsTheme.spacing.xxl),
                )
                LaunchedEffect(message) {
                    kotlinx.coroutines.delay(NOTICE_MILLIS)
                    viewModel.onNoticeShown()
                }
            }
        }
    }
    deleting?.let { draft ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text("Delete this draft?") },
            text = { Text("It can't be brought back.") },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.delete(draft.id)
                    deleting = null
                }) { Text("Delete", color = UsTheme.extended.statusDanger) }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text("Keep") } },
        )
    }
}

@Composable
private fun DraftRow(draft: PostDraft, thumb: String?, onOpen: () -> Unit, onDelete: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpen)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("post-draft:${draft.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .width(THUMB_WIDTH)
                .height(THUMB_HEIGHT)
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .background(UsTheme.extended.bgRaised),
        ) {
            if (thumb != null) {
                AsyncImage(model = thumb, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
            } else {
                Icon(UsIcons.FileText, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.align(Alignment.Center))
            }
        }
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = draftHeadline(draft),
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = draftMeta(draft, formatRelativeTime(draft.updatedAt)),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        IconButton(onClick = onDelete, modifier = Modifier.testTag("post-draft-delete:${draft.id}")) {
            Icon(UsIcons.Trash, contentDescription = "Delete draft", tint = UsTheme.extended.textMuted, modifier = Modifier.size(DELETE_ICON))
        }
    }
}

/** What a draft's row leads with: its title, else its words, else that it has none. Pure. */
fun draftHeadline(draft: PostDraft): String =
    draft.details.title?.takeIf { it.isNotBlank() } ?: draft.text.trim().takeIf { it.isNotEmpty() } ?: "No description"

/** "3 photos · 2h", "Text · now". Pure. */
fun draftMeta(draft: PostDraft, age: String): String {
    val kind = when (draft.mediaIds.size) {
        0 -> "Text"
        1 -> "1 photo"
        else -> "${draft.mediaIds.size} photos"
    }
    return if (age.isBlank()) kind else "$kind · $age"
}

private const val NOTICE_MILLIS = 3_000L
private val THUMB_WIDTH = 54.dp
private val THUMB_HEIGHT = 72.dp
private val DELETE_ICON = 20.dp
