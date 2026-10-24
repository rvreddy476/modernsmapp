package com.us.android.feature.feed.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.PersonSuggestion
import com.us.android.core.chat.data.SuggestionAction
import com.us.android.core.chat.data.SuggestionsRepository
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.media.data.MediaRepository
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.suggestionButton
import com.us.android.core.profile.data.FollowListApi
import com.us.android.core.profile.data.ProfileBatchRequest
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One person the Home feed suggests (2026-10-24, the web's PeopleYouMayKnowStrip). */
@Immutable
data class SuggestedPerson(
    val userId: String,
    val name: String,
    val username: String,
    val avatarMediaId: String?,
    /** The avatar's resolved address, when the asset is ready; null draws the initial. */
    val avatarUrl: String? = null,
    /** "3 mutual friends", the engine's own reason, or the handle. */
    val line: String,
)

/** The suggestion engine and the names behind it; a seam so the ViewModel tests against a fake. */
interface PeopleSuggestionsSource {
    suspend fun people(): AppResult<List<SuggestedPerson>>
    suspend fun hide(userId: String)
    suspend fun followed(userId: String)
    suspend fun shown(people: List<SuggestedPerson>)
}

/**
 * `GET /v1/suggestions/people` (suggestion-service handler.go:38, the
 * `{type, items:[{candidate_user_id, display_name, mutual_friend_count, explain_text, …}]}`
 * page, service/suggestion.go:50-75) with the profiles batch for names the
 * engine left blank, and the action and impression write-backs under the
 * `home` surface. The write-backs are telemetry: their failures are dropped.
 */
class DefaultPeopleSuggestionsSource @Inject constructor(
    private val suggestions: SuggestionsRepository,
    private val profiles: FollowListApi,
    private val media: MediaRepository,
) : PeopleSuggestionsSource {

    private var lastPage: List<PersonSuggestion> = emptyList()

    @Suppress("TooGenericExceptionCaught")
    override suspend fun people(): AppResult<List<SuggestedPerson>> {
        val page = when (val result = suggestions.people(PEOPLE_LIMIT)) {
            is AppResult.Failure -> return result
            is AppResult.Success -> result.data.filter { it.userId.isNotBlank() }.distinctBy { it.userId }
        }
        lastPage = page
        if (page.isEmpty()) return AppResult.Success(emptyList())
        val names = try {
            profiles.profiles(ProfileBatchRequest(page.map { it.userId }))
        } catch (e: CancellationException) {
            throw e
        } catch (e: Throwable) {
            emptyMap()
        }
        val people =
            page.mapNotNull { s ->
                val p = names[s.userId]
                // A person the batch left out (blocked, deactivated) is not suggested.
                if (names.isNotEmpty() && p == null) return@mapNotNull null
                val username = p?.username.orEmpty()
                SuggestedPerson(
                    userId = s.userId,
                    name = s.displayName.ifBlank { p?.displayName.orEmpty().ifBlank { username } },
                    username = username,
                    avatarMediaId = p?.avatarMediaId?.takeIf { it.isNotBlank() },
                    line = suggestionLine(s.mutualFriendCount, s.explainText, username, s.reasonCodes),
                )
            }
        // Each picture through `GET /v1/media/{id}/url`, like every other surface; a miss draws the initial.
        val withPictures = coroutineScope {
            people.map { person ->
                async {
                    val url = person.avatarMediaId?.let { id ->
                        (media.delivery(id) as? AppResult.Success)?.data?.takeIf { it.isReady }?.posterUrl
                    }
                    person.copy(avatarUrl = url)
                }
            }.awaitAll()
        }
        return AppResult.Success(withPictures)
    }

    override suspend fun hide(userId: String) {
        suggestions.personAction(userId, SuggestionAction.Hide, SuggestionsRepository.SURFACE_HOME)
    }

    override suspend fun followed(userId: String) {
        suggestions.personAction(userId, SuggestionAction.Follow, SuggestionsRepository.SURFACE_HOME)
    }

    override suspend fun shown(people: List<SuggestedPerson>) {
        val ids = people.mapTo(mutableSetOf()) { it.userId }
        suggestions.peopleImpression(lastPage.filter { it.userId in ids }, SuggestionsRepository.SURFACE_HOME)
    }

    private companion object {
        const val PEOPLE_LIMIT = 10
    }
}

/** suggestion-service's reason code for someone who follows the viewer and is not followed back (25 Oct 2026). */
internal const val REASON_FOLLOWS_YOU = "FOLLOWS_YOU"

/**
 * The line under a name (the web's lib/suggestionReason.ts): "Follows you" first
 * when the engine says so by its reason code (not by explain_text), with
 * " · N mutual friends" after it when there are any; else the mutual friends,
 * then the engine's reason, then the handle. Pure.
 */
internal fun suggestionLine(
    mutualCount: Int,
    explainText: String,
    username: String,
    reasonCodes: List<String> = emptyList(),
): String = when {
    REASON_FOLLOWS_YOU in reasonCodes ->
        if (mutualCount > 0) "Follows you · ${mutualFriends(mutualCount)}" else "Follows you"
    mutualCount > 0 -> mutualFriends(mutualCount)
    explainText.isNotBlank() -> explainText.trim()
    username.isNotBlank() -> "@$username"
    else -> "Suggested for you"
}

private fun mutualFriends(count: Int): String = "$count mutual friend" + if (count == 1) "" else "s"

@Immutable
data class PeopleYouMayKnowState(
    val people: List<SuggestedPerson> = emptyList(),
    val loaded: Boolean = false,
)

/**
 * People you may know on Home's For You (2026-10-24): one page of the pager,
 * after the first few posts, while the engine has anyone to suggest. Follow
 * goes through [FollowGraph] — the same lane the post header's Follow uses —
 * and Hide tells the engine and takes the person away. A failed read shows
 * no page at all; this is a suggestion, not content anyone asked for.
 */
@HiltViewModel
class PeopleYouMayKnowViewModel @Inject constructor(
    private val source: PeopleSuggestionsSource,
    private val follows: FollowGraph,
) : ViewModel() {

    private val _state = MutableStateFlow(PeopleYouMayKnowState())
    val state: StateFlow<PeopleYouMayKnowState> = _state.asStateFlow()

    /** The viewer's edges, so a row reads "Following" once followed. */
    val edges: StateFlow<Map<String, FollowStatus>> = follows.edges

    private var impressionSent = false

    init {
        viewModelScope.launch {
            val people = (source.people() as? AppResult.Success)?.data.orEmpty()
                .filter { it.userId != follows.ownId }
            _state.value = PeopleYouMayKnowState(people = people, loaded = true)
        }
    }

    /** The page came into view: one impression for the batch, once. */
    fun onShown() {
        val people = _state.value.people
        if (impressionSent || people.isEmpty()) return
        impressionSent = true
        viewModelScope.launch { source.shown(people) }
    }

    fun follow(userId: String) {
        viewModelScope.launch {
            if (follows.follow(userId) is AppResult.Success) source.followed(userId)
        }
    }

    fun hide(userId: String) {
        _state.update { s -> s.copy(people = s.people.filterNot { it.userId == userId }) }
        viewModelScope.launch { source.hide(userId) }
    }
}

/**
 * The page: a title and up to ten people, each with ONE button and a dismiss.
 * Drawn on the dark stage. The button is [suggestionButton]'s: Follow, or
 * Following / Requested once followed. No Message, no Connect, no Add friend
 * (founder, 24 Oct 2026); tapping the row opens the person's profile.
 */
@Composable
internal fun PeopleYouMayKnowPage(
    people: List<SuggestedPerson>,
    edges: Map<String, FollowStatus>,
    topInset: Dp,

    onOpenProfile: (String) -> Unit,
    onFollow: (String) -> Unit,
    onHide: (String) -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .padding(top = topInset)
            .testTag("feed_people_you_may_know"),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            Text(
                text = "People you may know",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.onMedia,
            )
            people.take(MAX_ROWS).forEach { person ->
                val name = person.name.ifBlank { "Someone" }
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable { onOpenProfile(person.userId) }
                        .testTag("feed_suggestion:${person.userId}"),
                ) {
                    UsAvatar(name = name, size = UsAvatarSize.Chat, seed = person.userId, imageUrl = person.avatarUrl)
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            text = name,
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = UsTheme.extended.onMedia,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            text = person.line,
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.onMediaMuted,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    val button = suggestionButton(edges[person.userId])
                    if (button.tappable) {
                        UsPillButton(button.label, onClick = { onFollow(person.userId) })
                    } else {
                        UsPillButton(button.label, filled = false, enabled = false, onClick = {})
                    }
                    IconButton(onClick = { onHide(person.userId) }, modifier = Modifier.size(DISMISS_TARGET)) {
                        Icon(UsIcons.Close, contentDescription = "Not interested in $name", tint = UsTheme.extended.onMediaMuted)
                    }
                }
            }
        }
    }
}

private const val MAX_ROWS = 10
private val DISMISS_TARGET = 40.dp
