package com.us.android.core.feed.ui.credits

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.credits.CreditEntry
import com.us.android.core.feed.data.credits.CreditLimits
import com.us.android.core.feed.data.credits.CreditPerson
import com.us.android.core.feed.data.credits.SuggestedCreditRoles
import com.us.android.core.model.CreditStatus

/**
 * The Credits section (video credits, 2026-10-23), drawn the same in the
 * upload's Credits step and in the owner's Edit sheet: cast & crew — a name,
 * a role (with the suggested roles one tap away), an optional link to a VChat
 * account through the people suggester — up to thirty, each movable up or
 * down and removable; then up to three genres, the release year and the
 * production house.
 *
 * A linked name says what happens next: "Will be asked to accept" for a new
 * link, or — on a saved video, to its owner — where the tag stands.
 */
@Composable
fun CreditsSection(
    state: CreditsEditorState,
    editor: CreditsEditor,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .testTag("credits_section"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        SectionHeading(
            title = "Cast & crew",
            count = "${state.form.entries.size}/${CreditLimits.MAX_CREDITS}",
        )
        if (state.form.entries.isEmpty()) {
            Text(
                text = "Credit the people in your video and who made it. Linked accounts are asked to accept.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        state.form.entries.forEachIndexed { index, entry ->
            CreditRow(
                entry = entry,
                position = index,
                count = state.form.entries.size,
                state = state,
                editor = editor,
                enabled = enabled,
            )
        }
        if (state.form.canAddCredit) {
            UsSecondaryButton(
                text = "Add person",
                onClick = editor::addCredit,
                enabled = enabled,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("credits_add"),
            )
        } else {
            Note("A video can credit up to ${CreditLimits.MAX_CREDITS} people.")
        }
        GenresBlock(state = state, editor = editor, enabled = enabled)
        UsTextField(
            value = state.form.releaseYear,
            onValueChange = editor::setYear,
            label = "Release year",
            placeholder = "2024",
            enabled = enabled,
            keyboardType = KeyboardType.Number,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("credits_year"),
        )
        UsTextField(
            value = state.form.productionHouse,
            onValueChange = editor::setProduction,
            label = "Production house",
            enabled = enabled,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("credits_production"),
        )
        state.problems.forEach { problem ->
            Text(
                text = problem,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.testTag("credits_problem"),
            )
        }
    }
}

@Composable
private fun SectionHeading(title: String, count: String) {
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Text(text = count, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textMuted)
    }
}

@Composable
private fun Note(text: String) {
    Text(text = text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
}

/** One credited person: the order controls, the name, the role, and the account link. */
@Composable
@Suppress("LongParameterList") // The row's own facts and the one editor behind every control.
private fun CreditRow(
    entry: CreditEntry,
    position: Int,
    count: Int,
    state: CreditsEditorState,
    editor: CreditsEditor,
    enabled: Boolean,
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(UsTheme.spacing.l)
            .testTag("credits_row:$position"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "${position + 1}",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.weight(1f),
            )
            RowControl(
                icon = UsIcons.ChevronDown,
                description = "Move up",
                enabled = enabled && position > 0,
                flipped = true,
                onClick = { editor.moveUp(entry.key) },
                testTag = "credits_up:$position",
            )
            RowControl(
                icon = UsIcons.ChevronDown,
                description = "Move down",
                enabled = enabled && position < count - 1,
                onClick = { editor.moveDown(entry.key) },
                testTag = "credits_down:$position",
            )
            RowControl(
                icon = UsIcons.Trash,
                description = "Remove ${entry.name.ifBlank { "this credit" }}",
                enabled = enabled,
                onClick = { editor.remove(entry.key) },
                testTag = "credits_remove:$position",
            )
        }
        UsTextField(
            value = entry.name,
            onValueChange = { editor.setName(entry.key, it) },
            label = "Name",
            enabled = enabled,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("credits_name:$position"),
        )
        UsTextField(
            value = entry.role,
            onValueChange = { editor.setRole(entry.key, it) },
            label = "Role",
            placeholder = "Actor, Director…",
            enabled = enabled,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("credits_role:$position"),
        )
        RoleSuggestions(selected = entry.role, enabled = enabled, onPick = { editor.setRole(entry.key, it) })
        LinkBlock(entry = entry, state = state, editor = editor, enabled = enabled)
    }
}

/** The suggested roles, a sideways row: one tap fills the role. */
@Composable
private fun RoleSuggestions(selected: String, enabled: Boolean, onPick: (String) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        SuggestedCreditRoles.forEach { role ->
            CreditChip(
                label = role,
                selected = selected.trim().equals(role, ignoreCase = true),
                enabled = enabled,
                onClick = { onPick(role) },
                testTag = "credits_role_chip:${role.lowercase()}",
            )
        }
    }
}

/** The account link: who it is and where the tag stands, the search while linking, or the way in. */
@Composable
private fun LinkBlock(entry: CreditEntry, state: CreditsEditorState, editor: CreditsEditor, enabled: Boolean) {
    val person = entry.person
    when {
        person != null -> LinkedPerson(
            person = person,
            status = entry.status,
            enabled = enabled,
            onUnlink = { editor.unlink(entry.key) },
        )
        state.linkingKey == entry.key -> LinkSearch(state = state, editor = editor, enabled = enabled)
        else -> TextAction(
            label = "Link a VChat account",
            enabled = enabled,
            onClick = { editor.startLinking(entry.key) },
            testTag = "credits_link:${entry.key}",
        )
    }
}

@Composable
private fun LinkedPerson(person: CreditPerson, status: CreditStatus?, enabled: Boolean, onUnlink: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(name = person.name, seed = person.id, size = UsAvatarSize.Small, imageUrl = person.avatarUrl)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = person.username.takeIf { it.isNotBlank() }?.let { "@$it" } ?: person.name,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = linkedStatusLine(status),
                style = MaterialTheme.typography.bodySmall,
                color = if (status == CreditStatus.ACCEPTED) UsTheme.extended.statusSuccess else UsTheme.extended.textMuted,
                modifier = Modifier.testTag("credits_status"),
            )
        }
        TextAction(label = "Unlink", enabled = enabled, onClick = onUnlink, testTag = "credits_unlink")
    }
}

/**
 * What a linked row says under the account. A link not yet saved will be
 * ASKED; a saved one says where it stands, to its owner — the only person
 * who is ever told.
 */
fun linkedStatusLine(status: CreditStatus?): String = when (status) {
    null -> "Will be asked to accept"
    CreditStatus.PENDING -> "Pending"
    CreditStatus.ACCEPTED -> "Accepted"
    CreditStatus.DECLINED -> "Declined"
    CreditStatus.UNLINKED -> "Not linked"
}

@Composable
private fun LinkSearch(state: CreditsEditorState, editor: CreditsEditor, enabled: Boolean) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        UsTextField(
            value = state.query,
            onValueChange = editor::onQuery,
            label = "Search people",
            placeholder = "Name or @username",
            enabled = enabled,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("credits_search"),
        )
        val query = state.query.trim()
        val hint = when {
            query.length < CreditsEditor.MIN_QUERY -> "Type a name or username."
            state.searching -> "Searching…"
            state.results.isEmpty() -> "No one found for \"$query\"."
            else -> null
        }
        hint?.let { Note(it) }
        state.results.take(MAX_RESULTS).forEach { person ->
            PersonResult(person = person, enabled = enabled, onClick = { editor.link(person) })
        }
        TextAction(label = "Cancel", enabled = true, onClick = editor::stopLinking, testTag = "credits_search_cancel")
    }
}

@Composable
private fun PersonResult(person: CreditPerson, enabled: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                enabled = enabled,
                onClick = onClick,
            )
            .semantics {
                role = Role.Button
                contentDescription = "Link ${person.name}"
            }
            .padding(vertical = UsTheme.spacing.s)
            .testTag("credits_result:${person.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(name = person.name, seed = person.id, size = UsAvatarSize.Small, imageUrl = person.avatarUrl)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = person.name,
                style = MaterialTheme.typography.bodyMedium,
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
    }
}

/** Up to three genres from the server's list; Retry when it could not be read. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun GenresBlock(state: CreditsEditorState, editor: CreditsEditor, enabled: Boolean) {
    SectionHeading(title = "Genres", count = "${state.form.genres.size}/${CreditLimits.MAX_GENRES}")
    when {
        state.genresLoading && state.genres.isEmpty() -> FlowRow(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            modifier = Modifier.semantics { contentDescription = "Loading genres" },
        ) {
            repeat(SKELETON_CHIPS) {
                Box(
                    modifier = Modifier
                        .size(width = SKELETON_CHIP_WIDTH, height = CHIP_HEIGHT)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(UsTheme.extended.fillSubtle),
                )
            }
        }
        state.genresFailed && state.genres.isEmpty() -> Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "Genres couldn't load.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.weight(1f),
            )
            TextAction(label = "Retry", enabled = true, onClick = editor::retryGenres, testTag = "credits_genres_retry")
        }
        else -> FlowRow(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            // A genre saved earlier that the list no longer carries is still shown, so it can be taken off.
            val known = state.genres.map { it.slug to it.label } +
                state.form.genres.filter { slug -> state.genres.none { it.slug == slug } }.map { it to it }
            known.forEach { (slug, label) ->
                val picked = slug in state.form.genres
                CreditChip(
                    label = label,
                    selected = picked,
                    enabled = enabled && (picked || state.form.canAddGenre),
                    onClick = { editor.toggleGenre(slug) },
                    testTag = "credits_genre:$slug",
                )
            }
        }
    }
}

/** A pill: white with navy text when picked — the app's selection rule. */
@Composable
private fun CreditChip(label: String, selected: Boolean, enabled: Boolean, onClick: () -> Unit, testTag: String) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .height(CHIP_HEIGHT)
            .clip(shape)
            .background(if (selected) UsTheme.extended.selectedPill else UsTheme.extended.glassBg)
            .border(HAIRLINE, if (selected) UsTheme.extended.selectedPill else UsTheme.extended.glassBorder, shape)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                enabled = enabled,
                onClick = onClick,
            )
            .semantics {
                role = Role.Checkbox
                this.selected = selected
            }
            .padding(horizontal = UsTheme.spacing.l)
            .testTag(testTag),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.SemiBold,
            color = when {
                selected -> UsTheme.extended.onSelectedPill
                enabled -> UsTheme.extended.textPrimary
                else -> UsTheme.extended.textGhost
            },
            maxLines = 1,
        )
    }
}

@Composable
@Suppress("LongParameterList") // A glyph button's facts; a holder would hide, not help.
private fun RowControl(
    icon: ImageVector,
    description: String,
    enabled: Boolean,
    onClick: () -> Unit,
    testTag: String,
    flipped: Boolean = false,
) {
    Box(
        modifier = Modifier
            .size(TAP_TARGET)
            .clip(CircleShape)
            .clickable(enabled = enabled, onClick = onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .testTag(testTag),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textGhost,
            modifier = Modifier
                .size(GLYPH)
                .then(if (flipped) Modifier.rotate(HALF_TURN) else Modifier),
        )
    }
}

@Composable
private fun TextAction(label: String, enabled: Boolean, onClick: () -> Unit, testTag: String) {
    Text(
        text = label,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.SemiBold,
        color = if (enabled) UsTheme.extended.accentSolid else UsTheme.extended.textGhost,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                enabled = enabled,
                onClick = onClick,
            )
            .semantics { role = Role.Button }
            .padding(vertical = UsTheme.spacing.s)
            .testTag(testTag),
    )
}

private const val MAX_RESULTS = 5
private const val SKELETON_CHIPS = 6
private const val HALF_TURN = 180f
private val HAIRLINE = 1.dp
private val CHIP_HEIGHT = 32.dp
private val SKELETON_CHIP_WIDTH = 72.dp
private val TAP_TARGET = 40.dp
private val GLYPH = 18.dp
