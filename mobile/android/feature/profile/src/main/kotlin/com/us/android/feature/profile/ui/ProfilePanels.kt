package com.us.android.feature.profile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.Profile
import com.us.android.core.profile.data.ConnectionPerson
import com.us.android.core.profile.data.ConnectionsSource
import com.us.android.core.profile.data.ProfileAbout
import com.us.android.core.profile.data.ProfilePanelsSource
import com.us.android.core.profile.data.RepostRow
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.rememberRichText
import com.us.android.feature.profile.navigation.ConnectionsRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import javax.inject.Inject

/** Where the panels' rows go; `:app` decides through [ProfileDestinations]. Null leaves a row inert. */
@Immutable
data class ProfilePanelDestinations(
    val onOpenPost: ((postId: String, contentType: String) -> Unit)? = null,
    val onOpenProfile: ((userId: String) -> Unit)? = null,
    val onOpenConnections: ((ConnectionsRoute) -> Unit)? = null,
)

/** One panel's read. */
@Immutable
data class PanelLoad<T>(
    val data: T? = null,
    val loading: Boolean = false,
    val error: String? = null,
)

@Immutable
data class RepostsLoad(
    val rows: List<RepostRow> = emptyList(),
    val loaded: Boolean = false,
    val loading: Boolean = false,
    val loadingMore: Boolean = false,
    val error: String? = null,
    val nextCursor: String? = null,
)

@Immutable
data class ProfilePanelsUiState(
    val about: PanelLoad<ProfileAbout> = PanelLoad(),
    val connections: PanelLoad<List<ConnectionPerson>> = PanelLoad(),
    val reposts: RepostsLoad = RepostsLoad(),
)

/**
 * Behind the profile's About, Connections and Reposts tabs (2026-10-24).
 * Each panel reads when it is first opened, and again on the profile's pull
 * to refresh; Reposts pages through its cursor.
 */
@HiltViewModel
class ProfilePanelsViewModel @Inject constructor(
    private val panels: ProfilePanelsSource,
    private val connections: ConnectionsSource,
) : ViewModel() {

    private val _state = MutableStateFlow(ProfilePanelsUiState())
    val state: StateFlow<ProfilePanelsUiState> = _state.asStateFlow()

    private var userId = ""

    /** [tab] is on screen for [owner]; [force] re-reads (pull to refresh). */
    fun open(owner: String, tab: ProfileGridTab, force: Boolean = false) {
        if (owner.isBlank()) return
        if (owner != userId) {
            userId = owner
            _state.value = ProfilePanelsUiState()
        }
        val s = _state.value
        when (tab) {
            ProfileGridTab.ABOUT -> if (force || (s.about.data == null && !s.about.loading)) loadAbout()
            ProfileGridTab.CONNECTIONS ->
                if (force || (s.connections.data == null && !s.connections.loading)) loadConnections()
            ProfileGridTab.REPOSTS -> if (force || (!s.reposts.loaded && !s.reposts.loading)) loadReposts()
            else -> Unit
        }
    }

    private fun loadAbout() {
        val owner = userId
        _state.update { it.copy(about = it.about.copy(loading = true, error = null)) }
        viewModelScope.launch {
            val result = panels.about(owner)
            if (owner != userId) return@launch
            _state.update {
                it.copy(
                    about = when (result) {
                        is AppResult.Success -> PanelLoad(data = result.data)
                        is AppResult.Failure -> it.about.copy(loading = false, error = ABOUT_FAILED)
                    },
                )
            }
        }
    }

    private fun loadConnections() {
        val owner = userId
        _state.update { it.copy(connections = it.connections.copy(loading = true, error = null)) }
        viewModelScope.launch {
            val result = connections.connections(owner, offset = 0, limit = CONNECTIONS_PREVIEW)
            if (owner != userId) return@launch
            _state.update {
                it.copy(
                    connections = when (result) {
                        is AppResult.Success -> PanelLoad(data = result.data)
                        is AppResult.Failure -> it.connections.copy(loading = false, error = CONNECTIONS_FAILED)
                    },
                )
            }
        }
    }

    private fun loadReposts() {
        val owner = userId
        _state.update { it.copy(reposts = it.reposts.copy(loading = true, error = null)) }
        viewModelScope.launch {
            val result = panels.reposts(owner)
            if (owner != userId) return@launch
            _state.update {
                it.copy(
                    reposts = when (result) {
                        is AppResult.Success ->
                            RepostsLoad(rows = result.data.items, loaded = true, nextCursor = result.data.nextCursor)
                        is AppResult.Failure -> it.reposts.copy(loading = false, error = REPOSTS_FAILED)
                    },
                )
            }
        }
    }

    /** The next page of Reposts; the rows on screen stay whatever the answer. */
    fun loadMoreReposts() {
        val owner = userId
        val now = _state.value.reposts
        val cursor = now.nextCursor ?: return
        if (now.loadingMore || now.loading) return
        _state.update { it.copy(reposts = it.reposts.copy(loadingMore = true)) }
        viewModelScope.launch {
            val result = panels.reposts(owner, cursor)
            if (owner != userId) return@launch
            _state.update { s ->
                val r = s.reposts
                s.copy(
                    reposts = when (result) {
                        is AppResult.Success -> {
                            val known = r.rows.mapTo(mutableSetOf()) { it.repostId }
                            r.copy(
                                rows = r.rows + result.data.items.filterNot { it.repostId in known },
                                nextCursor = result.data.nextCursor,
                                loadingMore = false,
                            )
                        }
                        is AppResult.Failure -> r.copy(loadingMore = false)
                    },
                )
            }
        }
    }

    companion object {
        /** How many connections the panel shows before "See all". */
        const val CONNECTIONS_PREVIEW = 12
        private const val ABOUT_FAILED = "Couldn't load About."
        private const val CONNECTIONS_FAILED = "Couldn't load connections."
        private const val REPOSTS_FAILED = "Couldn't load reposts."
    }
}

/** The panel for [tab]. */
@Composable
@Suppress("LongParameterList")
internal fun ProfilePanel(
    tab: ProfileGridTab,
    userId: String,
    isOwn: Boolean,
    profile: Profile?,
    destinations: ProfilePanelDestinations,
    refreshSeq: Int,
    viewModel: ProfilePanelsViewModel = hiltViewModel(key = "profile-panels:$userId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(userId, tab) { viewModel.open(userId, tab) }
    LaunchedEffect(refreshSeq) { if (refreshSeq > 0) viewModel.open(userId, tab, force = true) }
    Column(modifier = Modifier.fillMaxWidth().testTag("profile_panel:${tab.name.lowercase()}")) {
        when (tab) {
            ProfileGridTab.ABOUT -> AboutPanel(profile = profile, load = state.about, onRetry = { viewModel.open(userId, tab, force = true) })
            ProfileGridTab.CONNECTIONS -> ConnectionsPanel(
                load = state.connections,
                isOwn = isOwn,
                onOpenProfile = destinations.onOpenProfile,
                onSeeAll = destinations.onOpenConnections?.let { open ->
                    { open(ConnectionsRoute(userId = userId.takeUnless { isOwn }, handle = profile?.username.orEmpty())) }
                },
                onRetry = { viewModel.open(userId, tab, force = true) },
            )
            ProfileGridTab.REPOSTS -> RepostsPanel(
                load = state.reposts,
                onOpenPost = destinations.onOpenPost,
                onMore = viewModel::loadMoreReposts,
                onRetry = { viewModel.open(userId, tab, force = true) },
            )
            else -> Unit
        }
    }
}

// ── About ───────────────────────────────────────────────────────────────

@Composable
private fun AboutPanel(profile: Profile?, load: PanelLoad<ProfileAbout>, onRetry: () -> Unit) {
    val uri = LocalUriHandler.current
    val open: (String) -> Unit = { url -> if (isOpenableLink(url)) runCatching { uri.openUri(url.trim()) } }
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        profile?.bio?.takeIf { it.isNotBlank() }?.let { bio ->
            Text(
                text = rememberRichText(bio),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.testTag("about_bio"),
            )
        }
        profile?.let { p ->
            AboutLine(UsIcons.Profile, p.profession)
            AboutLine(UsIcons.MapPin, p.location)
            joinedLine(p.createdAt)?.let { AboutLine(UsIcons.Clock, it) }
            p.website.takeIf { isOpenableLink(it) }?.let { site ->
                AboutLine(UsIcons.Link, site.removePrefix("https://"), onClick = { open(site) }, tag = "about_website")
            }
        }
        when {
            load.error != null && load.data == null -> UsErrorState(message = load.error, onRetry = onRetry)
            load.data == null -> if (load.loading) UsLoadingState(label = "Loading About")
            else -> {
                load.data.links.forEach { link ->
                    AboutLine(UsIcons.Link, link.label, onClick = { open(link.url) }, tag = "about_link")
                }
                load.data.sections.forEach { section ->
                    Text(
                        text = sectionTitle(section.section),
                        style = MaterialTheme.typography.titleSmall,
                        fontWeight = FontWeight.SemiBold,
                        color = UsTheme.extended.textPrimary,
                    )
                    section.items.forEach { item ->
                        Column {
                            if (item.title.isNotBlank()) {
                                Text(item.title, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                            }
                            listOf(item.subtitle, item.detail).filter { it.isNotBlank() }.forEach {
                                Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                            }
                        }
                    }
                }
                val nothing = profile?.let { it.bio.isBlank() && it.profession.isBlank() && it.location.isBlank() } != false &&
                    load.data.links.isEmpty() && load.data.sections.isEmpty()
                if (nothing) UsEmptyState(title = "Nothing here yet", detail = null)
            }
        }
    }
}

@Composable
private fun AboutLine(icon: ImageVector, text: String, onClick: (() -> Unit)? = null, tag: String = "about_line") {
    if (text.isBlank()) return
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .testTag(tag),
    ) {
        Icon(icon, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(ABOUT_ICON))
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            color = if (onClick != null) UsTheme.extended.accentSolid else UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** Only an https link is handed to the browser from a profile. Pure, so it is a table test. */
internal fun isOpenableLink(url: String): Boolean {
    val trimmed = url.trim()
    return trimmed.startsWith("https://", ignoreCase = true) && trimmed.length > "https://".length && trimmed.none { it.isWhitespace() }
}

/** "Joined October 2026", from the profile's RFC 3339 `created_at`; null when it does not parse. */
internal fun joinedLine(createdAt: String): String? = runCatching {
    val date = Instant.parse(createdAt.trim()).atZone(ZoneId.of("UTC"))
    "Joined " + date.format(DateTimeFormatter.ofPattern("MMMM yyyy", Locale.US))
}.getOrNull()

/** A section's heading, in the web's words. */
internal fun sectionTitle(section: String): String = when (section) {
    "work" -> "Work"
    "education" -> "Education"
    "places" -> "Places lived"
    "contact" -> "Contact"
    "relationship" -> "Relationship"
    "family" -> "Family"
    "details" -> "Details"
    else -> section.replace('_', ' ').replaceFirstChar { it.uppercase() }
}

// ── Connections ─────────────────────────────────────────────────────────

@Composable
private fun ConnectionsPanel(
    load: PanelLoad<List<ConnectionPerson>>,
    isOwn: Boolean,
    onOpenProfile: ((String) -> Unit)?,
    onSeeAll: (() -> Unit)?,
    onRetry: () -> Unit,
) {
    val people = load.data
    when {
        load.error != null && people == null -> UsErrorState(message = load.error, onRetry = onRetry)
        people == null -> UsLoadingState(label = "Loading connections")
        people.isEmpty() -> UsEmptyState(
            title = if (isOwn) "No connections yet" else "No connections to show",
            detail = if (isOwn) "People you connect with will be listed here." else null,
        )
        else -> Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            people.forEach { person ->
                val name = person.displayName.ifBlank { person.username.ifBlank { "Someone" } }
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    modifier = Modifier
                        .fillMaxWidth()
                        .then(if (onOpenProfile != null) Modifier.clickable { onOpenProfile(person.userId) } else Modifier)
                        .testTag("profile_connection:${person.userId}"),
                ) {
                    UsAvatar(name = name, size = UsAvatarSize.Chat, seed = person.userId, imageUrl = rememberProfileMediaUrl(person.avatarMediaId))
                    Column(modifier = Modifier.weight(1f)) {
                        Text(name, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.textPrimary)
                        if (person.username.isNotBlank()) {
                            Text("@${person.username}", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                    }
                }
            }
            if (onSeeAll != null) {
                UsPillButton(text = "See all", filled = false, onClick = onSeeAll, modifier = Modifier.testTag("profile_connections_all"))
            }
        }
    }
}

// ── Reposts ─────────────────────────────────────────────────────────────

@Composable
private fun RepostsPanel(
    load: RepostsLoad,
    onOpenPost: ((String, String) -> Unit)?,
    onMore: () -> Unit,
    onRetry: () -> Unit,
) {
    when {
        load.error != null && load.rows.isEmpty() -> UsErrorState(message = load.error, onRetry = onRetry)
        !load.loaded -> UsLoadingState(label = "Loading reposts")
        load.rows.isEmpty() -> UsEmptyState(title = "No reposts yet", detail = null)
        else -> Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            load.rows.forEach { row ->
                Column(
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                    modifier = Modifier
                        .fillMaxWidth()
                        .then(if (onOpenPost != null) Modifier.clickable { onOpenPost(row.postId, row.contentType) } else Modifier)
                        .testTag("profile_repost:${row.repostId}"),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                        Icon(UsIcons.Repost, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(ABOUT_ICON))
                        Text(
                            text = repostByline(row),
                            style = MaterialTheme.typography.labelMedium,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                    if (row.quoteText.isNotBlank()) {
                        Text(rememberRichText(row.quoteText), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
                    }
                    if (row.excerpt.isNotBlank()) {
                        Text(
                            text = row.excerpt,
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textSecondary,
                            maxLines = EXCERPT_LINES,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.padding(start = UsTheme.spacing.m),
                        )
                    }
                }
            }
            if (load.nextCursor != null) {
                UsPillButton(text = "Show more", filled = false, busy = load.loadingMore, onClick = onMore)
            }
        }
    }
}

/** "Quoted Ana's post" / "Reposted Ana's post", or without the name when the server did not say. */
internal fun repostByline(row: RepostRow): String {
    val verb = if (row.quoteText.isNotBlank()) "Quoted" else "Reposted"
    return if (row.originalAuthor.isBlank()) "$verb a post" else "$verb ${row.originalAuthor}'s post"
}

private val ABOUT_ICON = 16.dp
private const val EXCERPT_LINES = 3
