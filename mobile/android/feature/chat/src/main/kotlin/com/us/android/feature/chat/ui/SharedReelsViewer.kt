package com.us.android.feature.chat.ui

import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import coil3.compose.AsyncImage
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.SHARED_POSTS
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsLoadingState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The order the viewer pages through (CONTRACT §1.4, §9): the chat's shared
 * posts newest first, starting at the tapped one. A tapped post the list
 * does not carry (older than what was read, or cleared) is put first rather
 * than lost. Pure, so it is a table test.
 */
fun sharedReelOrder(sharedPostIds: List<String>, tappedPostId: String): Pair<List<String>, Int> {
    val distinct = sharedPostIds.filter { it.isNotBlank() }.distinct()
    val index = distinct.indexOf(tappedPostId)
    return if (index >= 0) distinct to index else (listOf(tappedPostId) + distinct) to 0
}

data class SharedReelsUiState(
    val postIds: List<String> = emptyList(),
    val startIndex: Int = 0,
    val items: Map<String, FeedItem?> = emptyMap(),
    val loading: Boolean = true,
)

/**
 * The swipe viewer over a chat's shared posts. Its OWN player pool, built
 * from the app's player factory and authenticated media chain — never the
 * Reels tab's, so leaving it can release every decoder without touching
 * the tab's pages.
 */
@HiltViewModel
@OptIn(UnstableApi::class)
class SharedReelsViewModel @Inject constructor(
    private val parity: ChatParityRepository,
    private val posts: PostCardSource,
    private val urls: MediaUrlResolver,
    playerFactory: PlayerFactory,
    mediaSources: MediaSources,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {
    private val conversationId = savedStateHandle.get<String>("conversationId").orEmpty()
    private val tapped = savedStateHandle.get<String>("postId").orEmpty()

    val pool = PlayerPool(playerFactory, mediaSources)

    private val _state = MutableStateFlow(SharedReelsUiState(postIds = listOf(tapped)))
    val state: StateFlow<SharedReelsUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            val ids = mutableListOf<String>()
            var cursor: String? = null
            var pages = 0
            while (pages < MAX_PAGES) {
                val page = parity.shared(conversationId, SHARED_POSTS, cursor) as? AppResult.Success ?: break
                ids += page.data.items.mapNotNull { it.postId }
                pages++
                cursor = page.data.nextCursor ?: break
            }
            val (order, start) = sharedReelOrder(ids, tapped)
            _state.update { it.copy(postIds = order, startIndex = start, loading = false) }
            ensure(start)
        }
    }

    /** Reads the post at [index] and its neighbours, once each. */
    fun ensure(index: Int) {
        val ids = _state.value.postIds
        (index - 1..index + 1).mapNotNull { ids.getOrNull(it) }.forEach { postId ->
            if (postId in _state.value.items) return@forEach
            _state.update { it.copy(items = it.items + (postId to null)) }
            viewModelScope.launch {
                val item = (posts.load(postId) as? AppResult.Success)?.data
                _state.update { it.copy(items = it.items + (postId to item)) }
            }
        }
    }

    fun playback(item: FeedItem): Playback? = urls.playbackFor(item)

    fun still(item: FeedItem): String? = urls.videoThumb(item).url

    override fun onCleared() {
        pool.release()
        super.onCleared()
    }

    private companion object {
        const val MAX_PAGES = 5
    }
}

@OptIn(UnstableApi::class)
@Composable
fun SharedReelsScreen(
    onBack: () -> Unit,
    onOpenPost: (postId: String, contentType: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: SharedReelsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    Box(modifier = Modifier.fillMaxSize().background(UsTheme.extended.stage).testTag("shared_reels")) {
        if (state.loading) {
            UsLoadingState(label = "Loading")
        } else {
            val pager = rememberPagerState(initialPage = state.startIndex) { state.postIds.size }
            LaunchedEffect(pager.settledPage) {
                viewModel.ensure(pager.settledPage)
                viewModel.pool.playOnly(pager.settledPage)
            }
            DisposableEffect(Unit) { onDispose { viewModel.pool.pauseAll() } }
            VerticalPager(state = pager, modifier = Modifier.fillMaxSize()) { page ->
                val postId = state.postIds[page]
                val item = state.items[postId]
                Box(modifier = Modifier.fillMaxSize().testTag("shared_reel:$postId")) {
                    when {
                        item == null -> UsLoadingState(label = "Loading")
                        else -> {
                            val playback = remember(item.id) { viewModel.playback(item) }
                            if (playback != null) {
                                val player = remember(page, playback) { viewModel.pool.acquire(page, playback) }
                                LaunchedEffect(player) { player.repeatMode = Player.REPEAT_MODE_ONE }
                                PlayerSurface(player = player, surfaceType = SURFACE_TYPE_SURFACE_VIEW, modifier = Modifier.fillMaxSize())
                            } else {
                                AsyncImage(
                                    model = viewModel.still(item),
                                    contentDescription = null,
                                    contentScale = ContentScale.Fit,
                                    modifier = Modifier.fillMaxSize(),
                                )
                            }
                            Column(
                                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                                modifier = Modifier
                                    .align(Alignment.BottomStart)
                                    .navigationBarsPadding()
                                    .padding(UsTheme.spacing.xl),
                            ) {
                                TextButton(onClick = { onOpenAuthor(item.author.id) }) {
                                    Text(
                                        item.author.nameForDisplay,
                                        style = MaterialTheme.typography.titleSmall,
                                        fontWeight = FontWeight.Bold,
                                        color = UsTheme.extended.onMedia,
                                    )
                                }
                                val caption = item.title.ifBlank { item.text }.trim()
                                if (caption.isNotBlank()) {
                                    Text(caption, color = UsTheme.extended.onMedia, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                }
                                TextButton(onClick = { onOpenPost(item.id, item.feedContentType) }, modifier = Modifier.testTag("shared_reel_open")) {
                                    Text("Open post", color = UsTheme.extended.onMedia)
                                }
                            }
                        }
                    }
                }
            }
        }
        IconButton(
            onClick = onBack,
            modifier = Modifier
                .align(Alignment.TopStart)
                .statusBarsPadding()
                .padding(UsTheme.spacing.m)
                .testTag("shared_reels_close"),
        ) {
            Icon(UsIcons.Back, contentDescription = "Back", tint = UsTheme.extended.onMedia)
        }
    }
}
