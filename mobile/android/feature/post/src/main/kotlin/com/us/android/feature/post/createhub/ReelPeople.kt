package com.us.android.feature.post.createhub

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * "Tag people" — a full-screen search over users, multi-select up to
 * [MAX_TAGGED_PEOPLE].
 *
 * Drawn OVER the form rather than pushed as a route: the form's state is the
 * ViewModel's, and a second destination would need to share it. Back and
 * Done both return to the form with the picks kept; there is no cancel,
 * because a tag is removable from the chip under the row.
 */
@Composable
internal fun TagPeopleScreen(
    state: ReelPublishViewModel.ReelUiState,
    onQueryChanged: (String) -> Unit,
    onTag: (TaggedUser) -> Unit,
    onUntag: (String) -> Unit,
    onDone: () -> Unit,
) {
    BackHandler(onBack = onDone)
    val focus = remember { FocusRequester() }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .imePadding()
            .testTag("reel-tag-people"),
    ) {
        PeopleHeader(count = state.taggedUsers.size, onDone = onDone)
        Column(modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
            ReelInputField(
                value = state.peopleQuery,
                onValueChange = onQueryChanged,
                placeholder = "Search people",
                icon = UsIcons.Search,
                onDone = onDone,
                modifier = Modifier
                    .focusRequester(focus)
                    .testTag("reel-people-search"),
            )
            if (state.taggedUsers.isNotEmpty()) {
                Spacer(Modifier.height(UsTheme.spacing.l))
                TaggedChips(users = state.taggedUsers, onRemove = onUntag)
            }
            Spacer(Modifier.height(UsTheme.spacing.m))
        }
        PeopleResults(state = state, onTag = onTag, onUntag = onUntag)
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
}

/** Back, the title, and Done with the running count. */
@Composable
private fun PeopleHeader(count: Int, onDone: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(TAP_TARGET)
                .clip(CircleShape)
                .clickable(onClick = onDone)
                .semantics { contentDescription = "Back" },
            contentAlignment = Alignment.Center,
        ) {
            Icon(imageVector = UsIcons.Back, contentDescription = null, tint = UsTheme.extended.textPrimary)
        }
        Spacer(Modifier.width(UsTheme.spacing.s))
        Text(
            text = "Tag people",
            style = MaterialTheme.typography.titleMedium.copy(fontSize = TITLE_SIZE),
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        val interaction = remember { MutableInteractionSource() }
        Text(
            text = if (count > 0) "Done ($count)" else "Done",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.accentSolid,
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .clickable(interactionSource = interaction, indication = null, onClick = onDone)
                .pressDim(interaction)
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
                .testTag("reel-people-done"),
        )
    }
}

@Composable
private fun PeopleResults(
    state: ReelPublishViewModel.ReelUiState,
    onTag: (TaggedUser) -> Unit,
    onUntag: (String) -> Unit,
) {
    val query = state.peopleQuery.trim()
    val hint = when {
        query.length < MIN_QUERY -> "Search by name or username."
        state.peopleSearching -> "Searching…"
        state.peopleResults.isEmpty() -> "No one found for \"$query\"."
        !state.canTagMore -> "You can tag up to $MAX_TAGGED_PEOPLE people."
        else -> null
    }
    hint?.let {
        Text(
            text = it,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        )
    }
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.s,
        ),
    ) {
        items(state.peopleResults, key = { it.id }) { person ->
            val tagged = state.taggedUsers.any { it.id == person.id }
            PersonRow(
                person = person,
                tagged = tagged,
                enabled = tagged || state.canTagMore,
                onClick = { if (tagged) onUntag(person.id) else onTag(person) },
            )
        }
    }
}

/** Avatar, name over handle, and a ring that fills with the accent when picked. */
@Composable
private fun PersonRow(person: TaggedUser, tagged: Boolean, enabled: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.l)
            .semantics {
                role = Role.Checkbox
                selected = tagged
                contentDescription = "${person.name}${if (tagged) ", tagged" else ""}"
            }
            .testTag("reel-person-${person.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(name = person.name, size = UsAvatarSize.Post, seed = person.id)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = person.name,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (person.username.isNotBlank()) {
                Text(
                    text = "@${person.username}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Box(
            modifier = Modifier
                .size(RING_SIZE)
                .clip(CircleShape)
                .background(if (tagged) UsTheme.extended.accentSolid else Color.Transparent)
                .border(RING_STROKE, if (tagged) Color.Transparent else UsTheme.extended.borderMedium, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            if (tagged) {
                Icon(
                    imageVector = UsIcons.Check,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(RING_GLYPH),
                )
            }
        }
    }
}

/**
 * The tagged people as removable chips: avatar initial, name, and an × that
 * removes without opening the search. A scrolling row, so twenty names
 * never push the form down a screen.
 */
@Composable
internal fun TaggedChips(users: List<TaggedUser>, onRemove: (String) -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        users.forEach { user ->
            val shape = RoundedCornerShape(UsTheme.radii.full)
            Row(
                modifier = Modifier
                    .clip(shape)
                    .background(UsTheme.extended.glassBg)
                    .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
                    .padding(start = UsTheme.spacing.xs, end = UsTheme.spacing.m)
                    .padding(vertical = UsTheme.spacing.xs)
                    .testTag("reel-tag-chip-${user.id}"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                UsAvatar(name = user.name, size = UsAvatarSize.Small, seed = user.id)
                Text(
                    text = user.name,
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.widthIn(max = CHIP_NAME_MAX),
                )
                Icon(
                    imageVector = UsIcons.Close,
                    contentDescription = "Remove ${user.name}",
                    tint = UsTheme.extended.textMuted,
                    modifier = Modifier
                        .size(CHIP_CLOSE)
                        .clip(CircleShape)
                        .clickable { onRemove(user.id) },
                )
            }
        }
    }
}

// ── Metrics ─────────────────────────────────────────────────────────────

private const val MIN_QUERY = 2

private val TAP_TARGET = 40.dp
private val TITLE_SIZE = 17.sp
private val RING_SIZE = 22.dp
private val RING_STROKE = 1.5.dp
private val RING_GLYPH = 13.dp
private val HAIRLINE = 1.dp
private val CHIP_CLOSE = 16.dp
private val CHIP_NAME_MAX = 140.dp
