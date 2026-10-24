package com.us.android.feature.search.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.designsystem.theme.UsTypeScale
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.search.data.RecentSearch
import com.us.android.feature.search.data.SavedSearch
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.data.SearchSuggestion
import com.us.android.feature.search.navigation.SearchDestinations
import com.us.android.feature.search.navigation.SearchScope
import java.time.Instant

/**
 * Search (founder, 2026-09-05; T-16, 2026-10-15): the field on top with
 * the search glyph, a clear × once something is typed, focused on arrival
 * with the keyboard up and its action set to Search. Under it, one of three
 * bodies ([SearchBody]):
 *
 *  - the box empty: the signed-in viewer's recent searches — each with its
 *    own ×, and "Clear all" behind a confirmation — then popular searches;
 *  - typing: "Search for …" and autocomplete's suggestions;
 *  - searched: the tabs for where the page was opened from, and that tab's
 *    results, with loading, empty, and error-with-retry states.
 *
 * Every row opens what it names; `:app` resolves where that is.
 */
@Composable
fun SearchScreen(
    destinations: SearchDestinations,
    viewModel: SearchViewModel = hiltViewModel(),
) {
    val query by viewModel.query.collectAsStateWithLifecycle()
    val scope by viewModel.scope.collectAsStateWithLifecycle()
    val body by viewModel.body.collectAsStateWithLifecycle()
    val results by viewModel.results.collectAsStateWithLifecycle()
    val suggestions by viewModel.suggestions.collectAsStateWithLifecycle()
    val signedIn by viewModel.signedIn.collectAsStateWithLifecycle()
    val recent by viewModel.recent.collectAsStateWithLifecycle()
    val saved by viewModel.saved.collectAsStateWithLifecycle()
    val popular by viewModel.popular.collectAsStateWithLifecycle()
    val confirmClear by viewModel.confirmClear.collectAsStateWithLifecycle()
    val notice by viewModel.notice.collectAsStateWithLifecycle()
    val edges by viewModel.followEdges.collectAsStateWithLifecycle()
    val followBusy by viewModel.followBusy.collectAsStateWithLifecycle()
    val keyboard = LocalSoftwareKeyboardController.current

    val actions = remember(viewModel, destinations) {
        SearchRowActions(
            onOpenUser = { hit -> destinations.onOpenProfile(hit.id) },
            onFollow = { hit -> viewModel.follow(hit.id) },
            onUnfollow = { hit -> viewModel.unfollow(hit.id) },
            onOpenPost = { hit -> destinations.onOpenPost(hit.id) },
            onOpenVideo = { hit ->
                if (hit.isReel) {
                    viewModel.openReel(hit.id)
                    destinations.onOpenReels()
                } else {
                    destinations.onOpenVideo(hit.id)
                }
            },
            onOpenChannel = { hit -> destinations.onOpenChannel(hit.id) },
            onOpenHashtag = { hit -> destinations.onOpenHashtag(hit.tag) },
            onOpenSound = { hit -> destinations.onOpenSound(hit.id) },
        )
    }
    val submitAnd: (() -> Unit) -> Unit = remember(keyboard) {
        { submit ->
            keyboard?.hide()
            submit()
        }
    }

    UsScaffold(
        applyPageGutter = false,
        topBar = {
            Column(modifier = Modifier.statusBarsPadding()) {
                SearchField(
                    value = query,
                    placeholder = viewModel.origin.placeholder,
                    onValueChange = viewModel::onQueryChanged,
                    onClear = viewModel::onClear,
                    onBack = destinations.onBack,
                    onSubmit = { submitAnd(viewModel::onSubmit) },
                )
                if (body == SearchBody.Results) {
                    ScopeTabs(scopes = viewModel.scopes, selected = scope, onSelect = viewModel::onScopeSelected)
                    // Saved searches (2026-10-24): save the search on screen, or unsave it.
                    if (signedIn) {
                        val isSaved = savedMatch(saved, query) != null
                        UsPillButton(
                            text = if (isSaved) "Saved" else "Save search",
                            filled = !isSaved,
                            onClick = viewModel::toggleSaved,
                            modifier = Modifier
                                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                                .testTag("search_save"),
                        )
                    }
                }
            }
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when (body) {
                SearchBody.Start -> StartBody(
                    signedIn = signedIn,
                    recent = recent,
                    saved = saved,
                    onPickSaved = { item -> submitAnd { viewModel.onSavedPicked(item) } },
                    onRemoveSaved = viewModel::removeSaved,
                    popular = popular,
                    placeholder = viewModel.origin.placeholder,
                    onPickRecent = { item -> submitAnd { viewModel.onRecentPicked(item) } },
                    onRemoveRecent = viewModel::removeRecent,
                    onClearAll = viewModel::onClearAllRequested,
                    onPickPopular = { text -> submitAnd { viewModel.onPopularPicked(text) } },
                )
                SearchBody.Suggestions -> Suggestions(
                    typed = query.trim(),
                    suggestions = suggestions,
                    onSearch = { text -> submitAnd { viewModel.onSuggestionPicked(text) } },
                    onOpenPerson = { person -> destinations.onOpenProfile(person.userId) },
                    onOpenTag = { tag -> destinations.onOpenHashtag(tag) },
                )
                SearchBody.Results -> Results(
                    results = results,
                    query = query,
                    follow = FollowFacts(ownUserId = viewModel.ownUserId, edges = edges, busy = followBusy),
                    actions = actions,
                    onRetry = viewModel::retry,
                )
            }
            UsMessageHost(
                message = remember(notice) { notice?.let { UsMessage(it) } },
                onDismiss = viewModel::onNoticeShown,
            )
        }
    }

    if (confirmClear) {
        ClearAllSheet(onConfirm = viewModel::onClearAllConfirmed, onDismiss = viewModel::onClearAllDismissed)
    }
}

// ── The field ───────────────────────────────────────────────────────────

/**
 * Back, then the pill: the search glyph, the text, and × once there is
 * text to clear. Focus is requested once on arrival — the page exists to
 * be typed into.
 */
@Composable
private fun SearchField(
    value: String,
    placeholder: String,
    onValueChange: (String) -> Unit,
    onClear: () -> Unit,
    onBack: () -> Unit,
    onSubmit: () -> Unit,
) {
    val focus = remember { FocusRequester() }
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = UsTheme.spacing.s, end = UsTheme.spacing.pageHorizontal, top = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Glyph(icon = UsIcons.Back, description = "Back", onClick = onBack, tag = "search_back")
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            singleLine = true,
            textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
            cursorBrush = SolidColor(UsTheme.extended.accentSolid),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { onSubmit() }),
            modifier = Modifier
                .weight(1f)
                .height(FIELD_HEIGHT)
                .background(UsTheme.extended.bgRaised, shape)
                .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
                .padding(start = FIELD_PADDING_START, end = UsTheme.spacing.s)
                .focusRequester(focus)
                .semantics { contentDescription = placeholder }
                .testTag("search_field"),
            decorationBox = { inner ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    Icon(
                        imageVector = UsIcons.Search,
                        contentDescription = null,
                        tint = UsTheme.extended.textDim,
                        modifier = Modifier.size(FIELD_GLYPH),
                    )
                    Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.CenterStart) {
                        if (value.isEmpty()) {
                            Text(
                                text = placeholder,
                                style = MaterialTheme.typography.bodyLarge,
                                color = UsTheme.extended.textDim,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                        inner()
                    }
                    if (value.isNotEmpty()) {
                        Glyph(
                            icon = UsIcons.Close,
                            description = "Clear",
                            onClick = onClear,
                            tag = "search_clear",
                            size = CLEAR_TARGET,
                            glyph = CLEAR_GLYPH,
                        )
                    }
                }
            },
        )
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
}

/** A square target, the icon in [tint], no ripple — a dip on press. */
@Composable
private fun Glyph(
    icon: ImageVector,
    description: String,
    onClick: () -> Unit,
    tag: String,
    size: Dp = GLYPH_TARGET,
    glyph: Dp = GLYPH_SIZE,
    tint: Color = UsTheme.extended.textPrimary,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(size)
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .testTag(tag),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = tint,
            modifier = Modifier.size(glyph),
        )
    }
}

// ── The tabs ────────────────────────────────────────────────────────────

/** Glass pills; the selected one is WHITE with navy text — the bar's rule, selected is never the accent. */
@Composable
private fun ScopeTabs(scopes: List<SearchScope>, selected: SearchScope, onSelect: (SearchScope) -> Unit) {
    LazyRow(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("search_scopes"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
    ) {
        items(scopes, key = { it.name }) { scope ->
            ScopeTab(scope = scope, active = scope == selected, onClick = { onSelect(scope) })
        }
    }
}

@Composable
private fun ScopeTab(scope: SearchScope, active: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val fill = if (active) UsTheme.extended.selectedPill else UsTheme.extended.glassBg
    val outline = if (active) UsTheme.extended.selectedPill else UsTheme.extended.glassBorder
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .height(CHIP_HEIGHT)
            .background(fill, shape)
            .border(HAIRLINE, outline, shape)
            .pressScale(onClick)
            .semantics {
                role = Role.Tab
                this.selected = active
            }
            .padding(horizontal = CHIP_HORIZONTAL)
            .testTag("search_scope:${scope.name.lowercase()}"),
    ) {
        Text(
            text = scope.label,
            style = MaterialTheme.typography.labelLarge,
            fontSize = CHIP_TEXT,
            fontWeight = FontWeight.SemiBold,
            color = if (active) UsTheme.extended.onSelectedPill else UsTheme.extended.textPrimary,
            maxLines = 1,
        )
    }
}

// ── The empty box ───────────────────────────────────────────────────────

/** Recent searches (signed in only), then Trending searches; the hint when there is neither. */
@Composable
private fun StartBody(
    signedIn: Boolean,
    recent: List<RecentSearch>,
    saved: List<SavedSearch> = emptyList(),
    onPickSaved: (SavedSearch) -> Unit = {},
    onRemoveSaved: (SavedSearch) -> Unit = {},
    popular: List<String>,
    placeholder: String,
    onPickRecent: (RecentSearch) -> Unit,
    onRemoveRecent: (RecentSearch) -> Unit,
    onClearAll: () -> Unit,
    onPickPopular: (String) -> Unit,
) {
    val recents = if (signedIn) recent else emptyList()
    val saves = if (signedIn) saved else emptyList()
    if (recents.isEmpty() && popular.isEmpty() && saves.isEmpty()) {
        UsEmptyState(
            title = placeholder,
            detail = "Search for people, videos, reels, sounds and hashtags.",
            modifier = Modifier.testTag("search_hint"),
        )
        return
    }
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("search_start"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
    ) {
        if (saves.isNotEmpty()) {
            item(key = "saved_header") { SectionHeader(title = "Saved") }
            items(saves, key = { "saved:${it.id}" }) { item ->
                SavedRow(item = item, onPick = { onPickSaved(item) }, onRemove = { onRemoveSaved(item) })
            }
        }
        if (recents.isNotEmpty()) {
            item(key = "recent_header") {
                SectionHeader(title = "Recent", action = "Clear all", onAction = onClearAll, actionTag = "search_recents_clear")
            }
            items(recents, key = { "recent:${it.id}" }) { item ->
                RecentRow(item = item, onPick = { onPickRecent(item) }, onRemove = { onRemoveRecent(item) })
            }
        }
        if (popular.isNotEmpty()) {
            // "Trending searches" (2026-10-17): GET /v1/search/popular, under Recent.
            item(key = "popular_header") { SectionHeader(title = TRENDING_SEARCHES) }
            items(popular, key = { "popular:$it" }) { text ->
                QueryRow(
                    icon = UsIcons.Search,
                    text = text,
                    description = "Search $text",
                    tag = "search_popular:$text",
                    onClick = { onPickPopular(text) },
                )
            }
        }
    }
}

@Composable
private fun SectionHeader(
    title: String,
    action: String? = null,
    onAction: () -> Unit = {},
    actionTag: String = "",
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .weight(1f)
                .semantics { heading() },
        )
        if (action != null) {
            Text(
                text = action,
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textMuted,
                modifier = Modifier
                    .pressScale(onAction)
                    .semantics { role = Role.Button }
                    .padding(UsTheme.spacing.s)
                    .testTag(actionTag),
            )
        }
    }
}

/** A saved search: the bookmark, the words, and × to unsave it (2026-10-24). */
@Composable
private fun SavedRow(item: SavedSearch, onPick: () -> Unit, onRemove: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(modifier = Modifier.weight(1f)) {
            QueryRow(
                icon = UsIcons.BookmarkOutline,
                text = item.query,
                description = "Search ${item.query}",
                tag = "search_saved:${item.id}",
                onClick = onPick,
            )
        }
        Glyph(
            icon = UsIcons.Close,
            description = "Remove ${item.query} from saved searches",
            onClick = onRemove,
            tag = "search_saved_remove:${item.id}",
            size = CLEAR_TARGET,
            glyph = CLEAR_GLYPH,
            tint = UsTheme.extended.textMuted,
        )
    }
}

/** The clock, the words, and × to remove just this one. */
@Composable
private fun RecentRow(item: RecentSearch, onPick: () -> Unit, onRemove: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(modifier = Modifier.weight(1f)) {
            QueryRow(
                icon = UsIcons.Clock,
                text = item.query,
                description = "Search ${item.query}",
                tag = "search_recent:${item.id}",
                onClick = onPick,
            )
        }
        Glyph(
            icon = UsIcons.Close,
            description = "Remove ${item.query} from recent searches",
            onClick = onRemove,
            tag = "search_recent_remove:${item.id}",
            size = CLEAR_TARGET,
            glyph = CLEAR_GLYPH,
            tint = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun QueryRow(icon: ImageVector, text: String, description: String, tag: String, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.l)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(ROW_GLYPH),
        )
        Text(
            text = text,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * Confirming "Clear all", as a Momentum sheet from the bottom — the app's
 * one way of asking before something cannot be undone. Both ways out are
 * real buttons.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ClearAllSheet(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = MaterialTheme.colorScheme.scrim.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(top = UsTheme.spacing.xl, bottom = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding()
                .testTag("search_clear_confirm"),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = "Clear all recent searches?",
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "Your search history will be removed from your account. This can't be undone.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            UsButton(text = "Clear all", onClick = onConfirm, modifier = Modifier.fillMaxWidth())
            UsSecondaryButton(text = "Cancel", onClick = onDismiss, modifier = Modifier.fillMaxWidth())
        }
    }
}

// ── Typing ──────────────────────────────────────────────────────────────

/** "Search for …" first — the words as typed — then autocomplete's people, tags and queries. */
@Composable
private fun Suggestions(
    typed: String,
    suggestions: List<SearchSuggestion>,
    onSearch: (String) -> Unit,
    onOpenPerson: (SearchSuggestion.Person) -> Unit,
    onOpenTag: (String) -> Unit,
) {
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("search_suggestions"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
    ) {
        item(key = "search_for") {
            QueryRow(
                icon = UsIcons.Search,
                text = "Search for “$typed”",
                description = "Search for $typed",
                tag = "search_for",
                onClick = { onSearch(typed) },
            )
        }
        suggestionRows(suggestions, onSearch, onOpenPerson, onOpenTag)
    }
}

private fun LazyListScope.suggestionRows(
    suggestions: List<SearchSuggestion>,
    onSearch: (String) -> Unit,
    onOpenPerson: (SearchSuggestion.Person) -> Unit,
    onOpenTag: (String) -> Unit,
) {
    items(suggestions, key = { suggestionKey(it) }) { suggestion ->
        when (suggestion) {
            is SearchSuggestion.Person -> PersonSuggestion(suggestion, onClick = { onOpenPerson(suggestion) })
            is SearchSuggestion.Tag -> QueryRow(
                icon = UsIcons.Hash,
                text = "#${suggestion.tag}",
                description = "Hashtag ${suggestion.tag}",
                tag = "search_suggestion_tag:${suggestion.tag}",
                onClick = { onOpenTag(suggestion.tag) },
            )
            is SearchSuggestion.Query -> QueryRow(
                icon = UsIcons.Search,
                text = suggestion.text,
                description = "Search ${suggestion.text}",
                tag = "search_suggestion:${suggestion.text}",
                onClick = { onSearch(suggestion.text) },
            )
        }
    }
}

private fun suggestionKey(suggestion: SearchSuggestion): String = when (suggestion) {
    is SearchSuggestion.Person -> "person:${suggestion.userId}"
    is SearchSuggestion.Tag -> "tag:${suggestion.tag}"
    is SearchSuggestion.Query -> "query:${suggestion.text}"
}

@Composable
private fun PersonSuggestion(person: SearchSuggestion.Person, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = person.nameForDisplay
            }
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m)
            .testTag("search_suggestion_person:${person.userId}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(name = person.nameForDisplay, seed = person.userId, size = UsAvatarSize.Small)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = person.nameForDisplay,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            person.handle?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

// ── Results ─────────────────────────────────────────────────────────────

@Composable
private fun Results(
    results: SearchResults,
    query: String,
    follow: FollowFacts,
    actions: SearchRowActions,
    onRetry: () -> Unit,
) {
    val now = remember(results) { Instant.now() }
    when (results) {
        SearchResults.Idle, SearchResults.Loading -> UsLoadingState(label = "Searching")
        is SearchResults.Error -> UsErrorState(message = results.message, onRetry = onRetry)
        is SearchResults.Loaded -> if (results.hits.isEmpty()) {
            UsEmptyState(
                title = "No ${emptyNoun(results.request.scope)} for “${query.trim()}”",
                detail = "Try other words, or another tab.",
                modifier = Modifier.testTag("search_empty"),
            )
        } else {
            HitList(hits = results.hits, follow = follow, actions = actions, now = now)
        }
    }
}

/** What "nothing found" names, per tab. */
internal fun emptyNoun(scope: SearchScope): String = when (scope) {
    SearchScope.TOP -> "results"
    else -> scope.label.lowercase()
}

@Composable
private fun HitList(hits: List<SearchHit>, follow: FollowFacts, actions: SearchRowActions, now: Instant) {
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("search_results"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        items(hits, key = { "${it::class.simpleName}:${it.id}" }) { hit ->
            SearchHitRow(hit = hit, follow = follow, actions = actions, now = now)
        }
    }
}

/** The empty box's second list, the platform's popular searches. */
internal const val TRENDING_SEARCHES = "Trending searches"

private const val SCRIM_ALPHA = 0.55f
private val SHEET_RADIUS = 28.dp
private val HAIRLINE = 1.dp
private val FIELD_HEIGHT = 46.dp
private val FIELD_PADDING_START = 16.dp
private val FIELD_GLYPH = 20.dp
private val GLYPH_TARGET = 44.dp
private val GLYPH_SIZE = 24.dp
private val CLEAR_TARGET = 32.dp
private val CLEAR_GLYPH = 18.dp
private val CHIP_HEIGHT = 34.dp
private val CHIP_HORIZONTAL = 14.dp
private val CHIP_TEXT = UsTypeScale.sm.size
private val ROW_GLYPH = 18.dp
