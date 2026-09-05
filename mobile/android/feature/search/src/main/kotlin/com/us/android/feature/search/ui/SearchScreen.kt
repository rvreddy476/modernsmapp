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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.navigation.SearchDestinations
import com.us.android.feature.search.navigation.SearchScope
import java.time.Instant

/**
 * Search (founder, 2026-09-05): the field on top with the search glyph,
 * a clear × once something is typed, focused on arrival with the keyboard
 * up and its action set to Search; under it the scope chips for where the
 * page was opened from; under those the results — or, while the field is
 * empty, the last ten searches with "Clear all". Every row opens what it
 * names; `:app` resolves where that is.
 */
@Composable
fun SearchScreen(
    destinations: SearchDestinations,
    viewModel: SearchViewModel = hiltViewModel(),
) {
    val query by viewModel.query.collectAsStateWithLifecycle()
    val scope by viewModel.scope.collectAsStateWithLifecycle()
    val results by viewModel.results.collectAsStateWithLifecycle()
    val recent by viewModel.recent.collectAsStateWithLifecycle()
    val edges by viewModel.followEdges.collectAsStateWithLifecycle()
    val followBusy by viewModel.followBusy.collectAsStateWithLifecycle()
    val keyboard = LocalSoftwareKeyboardController.current

    val actions = remember(viewModel, destinations) {
        SearchRowActions(
            onOpenUser = { hit ->
                viewModel.onHitOpened()
                destinations.onOpenProfile(hit.id)
            },
            onFollow = { hit -> viewModel.follow(hit.id) },
            onOpenPost = { hit ->
                viewModel.onHitOpened()
                destinations.onOpenPost(hit.id)
            },
            onOpenVideo = { hit ->
                viewModel.onHitOpened()
                if (hit.isReel) {
                    viewModel.openReel(hit.id)
                    destinations.onOpenReels()
                } else {
                    destinations.onOpenVideo(hit.id)
                }
            },
            onOpenChannel = { hit ->
                viewModel.onHitOpened()
                destinations.onOpenChannel(hit.id)
            },
        )
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
                    onSubmit = {
                        keyboard?.hide()
                        viewModel.onSubmit()
                    },
                )
                ScopeChips(scopes = viewModel.scopes, selected = scope, onSelect = viewModel::onScopeSelected)
            }
        },
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            when {
                query.isBlank() -> Recents(
                    recent = recent,
                    onPick = viewModel::onRecentPicked,
                    onClearAll = viewModel::onClearRecents,
                    placeholder = viewModel.origin.placeholder,
                )
                else -> Results(
                    results = results,
                    query = query,
                    follow = FollowFacts(ownUserId = viewModel.ownUserId, edges = edges, busy = followBusy),
                    actions = actions,
                    onRetry = viewModel::retry,
                )
            }
        }
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

/** A square target, the icon in white, no ripple — a dip on press. */
@Composable
private fun Glyph(
    icon: ImageVector,
    description: String,
    onClick: () -> Unit,
    tag: String,
    size: Dp = GLYPH_TARGET,
    glyph: Dp = GLYPH_SIZE,
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
        Icon(imageVector = icon, contentDescription = null, tint = Color.White, modifier = Modifier.size(glyph))
    }
}

// ── The chips ───────────────────────────────────────────────────────────

/** Glass pills; the selected one is WHITE with navy text — the bar's rule, selected is never the accent. */
@Composable
private fun ScopeChips(scopes: List<SearchScope>, selected: SearchScope, onSelect: (SearchScope) -> Unit) {
    LazyRow(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("search_scopes"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
    ) {
        items(scopes, key = { it.name }) { scope ->
            ScopeChip(scope = scope, active = scope == selected, onClick = { onSelect(scope) })
        }
    }
}

@Composable
private fun ScopeChip(scope: SearchScope, active: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val fill = if (active) Color.White else UsTheme.extended.glassBg
    val outline = if (active) Color.White else UsTheme.extended.glassBorder
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
            color = if (active) UsTheme.extended.brandNavy else UsTheme.extended.textPrimary,
            maxLines = 1,
        )
    }
}

// ── The body ────────────────────────────────────────────────────────────

/** The last searches while the field is empty; the hint when there are none yet. */
@Composable
private fun Recents(
    recent: List<String>,
    onPick: (String) -> Unit,
    onClearAll: () -> Unit,
    placeholder: String,
) {
    if (recent.isEmpty()) {
        UsEmptyState(
            title = placeholder,
            detail = "Type at least ${SearchQueries.MIN_CHARS} characters.",
            modifier = Modifier.testTag("search_hint"),
        )
        return
    }
    LazyColumn(
        modifier = Modifier
            .fillMaxSize()
            .testTag("search_recents"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
    ) {
        item(key = "header") {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = "Recent",
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = "Clear all",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier
                        .pressScale(onClearAll)
                        .semantics { role = Role.Button }
                        .padding(UsTheme.spacing.s)
                        .testTag("search_recents_clear"),
                )
            }
        }
        items(recent, key = { it }) { query ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .pressScale { onPick(query) }
                    .semantics {
                        role = Role.Button
                        contentDescription = "Search $query"
                    }
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.l)
                    .testTag("search_recent:$query"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                Icon(
                    imageVector = UsIcons.Clock,
                    contentDescription = null,
                    tint = UsTheme.extended.textMuted,
                    modifier = Modifier.size(RECENT_GLYPH),
                )
                Text(
                    text = query,
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

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
        SearchResults.Idle -> UsEmptyState(
            title = "Keep typing",
            detail = "Type at least ${SearchQueries.MIN_CHARS} characters.",
            modifier = Modifier.testTag("search_hint"),
        )
        SearchResults.Loading -> UsLoadingState(label = "Searching")
        is SearchResults.Error -> UsErrorState(message = results.message, onRetry = onRetry)
        is SearchResults.Loaded -> if (results.hits.isEmpty()) {
            UsEmptyState(
                title = "No ${results.request.scope.label.lowercase()} for “${query.trim()}”",
                detail = "Try another word, or another chip.",
                modifier = Modifier.testTag("search_empty"),
            )
        } else {
            HitList(hits = results.hits, follow = follow, actions = actions, now = now)
        }
    }
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
private val CHIP_TEXT = 13.sp
private val RECENT_GLYPH = 18.dp
