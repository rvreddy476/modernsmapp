package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import coil3.compose.AsyncImage
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/*
  A shared post in a chat thread (2026-10-17). message-service sends only
  `post_id` on a `type:"post"` message — the card's whole content is the post
  itself — so the thread reads the post (`GET /v1/posts/:id`, through the same
  hydrating read every post surface uses) and draws its still, its author and
  its caption. A post the viewer may not see, or one that is gone, is a card
  that says so; it never opens.
*/

/** What a post card shows: the still, who posted it, the first words, and where it opens. */
data class PostCard(
    val postId: String,
    val authorName: String,
    val caption: String,
    val thumbnailUrl: String?,
    val contentType: String,
) {
    /** A reel or a long video: the still wears a play glyph. */
    val isVideo: Boolean get() = contentType in VIDEO_TYPES
}

/** One card's read: still loading, ready, or not available to this viewer. */
sealed interface PostCardState {
    data object Loading : PostCardState
    data class Ready(val card: PostCard) : PostCardState
    data object Unavailable : PostCardState
}

/** [item] as a card, with the still [thumbnailUrl] the caller resolved. Pure, so the mapping is a table test. */
fun postCardOf(item: FeedItem, thumbnailUrl: String?): PostCard = PostCard(
    postId = item.id,
    authorName = item.author.nameForDisplay,
    caption = item.title.ifBlank { item.text }.trim(),
    thumbnailUrl = thumbnailUrl?.takeIf { it.isNotBlank() },
    contentType = item.feedContentType,
)

/** Reads one post for a card. A seam so the card ViewModel is tested without the network. */
fun interface PostCardSource {
    suspend fun load(postId: String): AppResult<FeedItem>
}

/** The card source is the hydrating single-post read every post surface uses. */
@Module
@InstallIn(SingletonComponent::class)
object PostCardModule {
    @Provides
    fun providePostCardSource(feeds: FeedRepository): PostCardSource = PostCardSource { feeds.post(it) }
}

/**
 * The thread's post cards, one read per post however many times it was
 * shared into the thread, kept for the screen's life.
 */
@HiltViewModel
class PostCardsViewModel @Inject constructor(
    private val source: PostCardSource,
    private val urls: MediaUrlResolver,
) : ViewModel() {

    private val _cards = MutableStateFlow<Map<String, PostCardState>>(emptyMap())
    val cards: StateFlow<Map<String, PostCardState>> = _cards.asStateFlow()

    /** Reads [postId]'s card unless it is read or being read. */
    fun ensure(postId: String) {
        if (postId.isBlank() || postId in _cards.value) return
        _cards.update { it + (postId to PostCardState.Loading) }
        viewModelScope.launch {
            val next = when (val result = source.load(postId)) {
                is AppResult.Success -> PostCardState.Ready(postCardOf(result.data, stillOf(result.data)))
                is AppResult.Failure -> PostCardState.Unavailable
            }
            _cards.update { it + (postId to next) }
        }
    }

    /** A video's cover or still, else a photo's first image at card size. */
    private fun stillOf(item: FeedItem): String? = urls.videoThumb(item).url
        ?: item.media.firstOrNull()?.let { urls.bestVariant(it.variants, CARD_IMAGE_HEIGHT) ?: urls.thumbnail(it.variants) }
}

/** What a message row needs to draw a post card: its read so far, how to ask for it, and where a tap goes. */
internal class PostCardSlot(
    val state: PostCardState?,
    val onEnsure: (String) -> Unit,
    val onOpen: (PostCard) -> Unit,
)

/**
 * The card inside a message bubble: the still (with a play glyph on a
 * video), the author and two lines of the caption. A tap opens the post;
 * a post that cannot be read says so and does nothing.
 */
@Composable
internal fun PostCardContent(
    postId: String,
    state: PostCardState?,
    lightGround: Boolean,
    onEnsure: (String) -> Unit,
    onOpen: (PostCard) -> Unit,
) {
    LaunchedEffect(postId) { onEnsure(postId) }
    val ink = if (lightGround) UsTheme.extended.onChatBubbleOwn else UsTheme.extended.textPrimary
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    val card = (state as? PostCardState.Ready)?.card
    Column(
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .fillMaxWidth()
            .then(
                if (card != null) {
                    Modifier
                        .clickable { onOpen(card) }
                        .semantics {
                            role = Role.Button
                            contentDescription = "Post by ${card.authorName}"
                        }
                } else {
                    Modifier
                },
            )
            .testTag("thread-post-card:$postId"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(CARD_ASPECT)
                .clip(shape)
                .background(UsTheme.extended.bgCard),
        ) {
            card?.thumbnailUrl?.let { url ->
                AsyncImage(
                    model = url,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.matchParentSizeIn(),
                )
            }
            if (card?.isVideo == true) {
                Icon(
                    imageVector = UsIcons.Play,
                    contentDescription = null,
                    tint = UsTheme.extended.onMedia,
                    modifier = Modifier.size(PLAY_GLYPH),
                )
            }
        }
        when (state) {
            is PostCardState.Ready -> {
                Text(
                    text = state.card.authorName,
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = ink,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (state.card.caption.isNotEmpty()) {
                    Text(
                        text = state.card.caption,
                        style = MaterialTheme.typography.bodySmall,
                        color = ink,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            PostCardState.Unavailable -> Text(
                text = POST_UNAVAILABLE,
                style = MaterialTheme.typography.bodySmall,
                color = ink,
                modifier = Modifier.padding(top = UsTheme.spacing.xs),
            )
            PostCardState.Loading, null -> Text(
                text = "Shared a post",
                style = MaterialTheme.typography.bodySmall,
                color = ink,
            )
        }
    }
}

/** Fills the card's still box; a named helper so the image sits exactly over the ground. */
private fun Modifier.matchParentSizeIn(): Modifier = this.fillMaxWidth().aspectRatio(CARD_ASPECT)

/** What a card says for a post the viewer cannot see (deleted, private, blocked). */
internal const val POST_UNAVAILABLE = "This post isn't available"

private val VIDEO_TYPES = setOf("flick", "reel", "short", "long_video")
private const val CARD_ASPECT = 4f / 5f
private const val CARD_IMAGE_HEIGHT = 720
private val PLAY_GLYPH = 32.dp
