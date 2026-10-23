package com.us.android.core.feed.ui.action

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionDraft
import com.us.android.core.model.ActionErrorCode
import com.us.android.core.model.ActionLabel
import com.us.android.core.model.ActionProblem
import com.us.android.core.model.ActionStatus
import com.us.android.core.model.UrlCheck
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSelectRow

/**
 * The label picker's options, for every editor under it. The contract's list
 * unless [ProvideActionLabels] has read the server's.
 */
val LocalActionLabels = staticCompositionLocalOf { ActionButtons.LABELS }

/**
 * Reads `GET /v1/posts/action-labels` once for the whole app and hands it to
 * every [ActionEditor] below. Placed at the app's root, under its Activity.
 */
@Composable
fun ProvideActionLabels(
    viewModel: ActionLabelsViewModel = hiltViewModel(key = "action-labels"),
    content: @Composable () -> Unit,
) {
    val labels by viewModel.labels.collectAsStateWithLifecycle()
    CompositionLocalProvider(LocalActionLabels provides labels, content = content)
}

/** Where the button will show, for the hint under "Add action button". */
enum class ActionSurface(val hint: String) {
    Post("A button under your post that opens a website, like Apply now or Book now."),
    Video("A button under your video that opens a website, like Apply now or Book now."),
    Reel("A button above your reel's caption that opens a website, like Apply now or Book now."),
    Profile("A button next to Follow and Message that opens a website, like Book now or Shop now."),
}

/**
 * "Add action button" (2026-10-23): a label from the fixed list and a link,
 * or nothing. One editor for the reel and long-video publish form, the text
 * composer, the photo studio's details, the Edit sheet and the profile.
 *
 * The link is checked as it is typed against the server's own rules
 * ([ActionButtons.checkUrl]), so a shortener or a group invite is refused
 * here, not by a failed publish. A saved button that is blocked or under
 * review says so ([savedStatus]); the server's last refusal
 * ([serverProblem]) is shown under it, ACTION_NOT_ELIGIBLE one missing item
 * per line. The labels are [LocalActionLabels]: `GET /v1/posts/action-labels`
 * once the app root ([ProvideActionLabels]) has read it, the contract's list
 * until then — so a screen under test needs no Hilt to draw the editor.
 */
@Composable
fun ActionEditor(
    value: ActionDraft?,
    onChange: (ActionDraft?) -> Unit,
    modifier: Modifier = Modifier,
    surface: ActionSurface = ActionSurface.Post,
    savedStatus: ActionStatus? = null,
    /** The saved button's `status_reason` (unsafe, moderator, reports), when the server said. */
    savedReason: String? = null,
    serverProblem: ActionProblem? = null,
    enabled: Boolean = true,
    showErrors: Boolean = false,
) {
    ActionEditorView(
        value = value,
        onChange = onChange,
        labels = LocalActionLabels.current,
        modifier = modifier,
        surface = surface,
        savedStatus = savedStatus,
        savedReason = savedReason,
        serverProblem = serverProblem,
        enabled = enabled,
        showErrors = showErrors,
    )
}

/** The editor without its label read, for hosts and previews that already hold the list. */
@Suppress("LongParameterList", "LongMethod") // One parameter per thing the editor shows.
@Composable
fun ActionEditorView(
    value: ActionDraft?,
    onChange: (ActionDraft?) -> Unit,
    labels: List<ActionLabel>,
    modifier: Modifier = Modifier,
    surface: ActionSurface = ActionSurface.Post,
    savedStatus: ActionStatus? = null,
    /** The saved button's `status_reason` (unsafe, moderator, reports), when the server said. */
    savedReason: String? = null,
    serverProblem: ActionProblem? = null,
    enabled: Boolean = true,
    showErrors: Boolean = false,
) {
    val status = ActionButtons.statusNote(savedStatus, savedReason)
    val check = value?.takeIf { it.url.isNotBlank() || showErrors }?.let { ActionButtons.checkUrl(it.url) }
    val urlProblem = (check as? UrlCheck.Bad)?.message
    Column(
        modifier = modifier.fillMaxWidth().testTag("action_editor"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        if (status != null) {
            Note(
                title = status.title,
                lines = listOf(status.detail),
                warning = true,
                tag = "action_status:${savedStatus?.wire}",
            )
        }
        if (value == null) {
            AddRow(enabled = enabled, hint = surface.hint) { onChange(ActionDraft(labels.firstOrNull()?.value.orEmpty(), "")) }
        } else {
            UsSettingsSelectRow(
                title = "Button says",
                selected = value.label,
                options = labels.map { UsSettingsOption(it.value, it.label) } +
                    listOfNotNull(
                        // A saved label the list no longer offers still reads as itself, and must be changed.
                        value.label.takeIf { l -> l.isNotBlank() && labels.none { it.value == l } }
                            ?.let { UsSettingsOption(it, "A label you can't use any more") },
                    ),
                onSelected = { onChange(value.copy(label = it)) },
                enabled = enabled,
                modifier = Modifier.testTag("action_label"),
            )
            UsTextField(
                value = value.url,
                onValueChange = { onChange(value.copy(url = it)) },
                label = "Link",
                placeholder = "https://",
                errorText = urlProblem,
                enabled = enabled,
                keyboardType = KeyboardType.Uri,
                modifier = Modifier.fillMaxWidth().testTag("action_url"),
            )
            if (urlProblem == null) {
                val ok = check as? UrlCheck.Ok
                Text(
                    text = if (ok != null) {
                        val words = labels.firstOrNull { it.value == value.label }?.label ?: "Your button"
                        "Viewers see it as “$words · ${ok.domain}” and open it through VChat's leaving page."
                    } else {
                        "Short links and WhatsApp or Telegram group invites can't be used."
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
            Row(
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.pill))
                    .clickable(enabled = enabled, role = Role.Button) { onChange(null) }
                    .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.s)
                    .testTag("action_remove"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Icon(imageVector = UsIcons.Trash, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(GLYPH))
                Text(
                    text = "Remove action button",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
        if (serverProblem != null) ProblemNote(serverProblem)
    }
}

/** A refusal from the server; ACTION_NOT_ELIGIBLE lists what is missing, one line each. */
@Composable
fun ProblemNote(problem: ActionProblem) {
    val missing = if (problem.code == ActionErrorCode.ACTION_NOT_ELIGIBLE) ActionButtons.missingSentences(problem.missing, problem.eligibleAt) else emptyList()
    if (missing.isNotEmpty()) {
        Note(title = "You can't add an action button yet:", lines = missing.map { "• $it" }, warning = false, tag = "action_problem:${problem.code.name}")
    } else {
        Note(title = null, lines = listOf(problem.message), warning = false, tag = "action_problem:${problem.code.name}")
    }
}

@Composable
private fun AddRow(enabled: Boolean, hint: String, onAdd: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(enabled = enabled, role = Role.Button, onClick = onAdd)
            .padding(vertical = UsTheme.spacing.l)
            .testTag("action_add"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(imageVector = UsIcons.Link, contentDescription = null, tint = UsTheme.extended.textPrimary)
        Column(modifier = Modifier.weight(1f)) {
            Text(text = "Add action button", style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            Text(text = hint, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
        Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
    }
}

@Composable
private fun Note(title: String?, lines: List<String>, warning: Boolean, tag: String) {
    val tone = if (warning) UsTheme.extended.statusWarning else UsTheme.extended.statusDanger
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.fillSubtle, shape)
            .border(1.dp, tone, shape)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s)
            .testTag(tag),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(
            imageVector = if (warning) UsIcons.ShieldAlert else UsIcons.Info,
            contentDescription = null,
            tint = tone,
            modifier = Modifier.size(GLYPH),
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            if (title != null) {
                Text(text = title, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.textPrimary)
            }
            lines.forEach { Text(text = it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
        }
    }
}

/** The one-line summary for a collapsed row: "Apply now · epfindia.gov.in", or the call to add one. */
fun actionSummary(value: ActionDraft?, labels: List<ActionLabel> = ActionButtons.LABELS): String {
    if (value == null) return "Add a button that opens a website"
    val label = labels.firstOrNull { it.value == value.label }?.label ?: "Action button"
    val check = ActionButtons.checkUrl(value.url)
    return if (check is UrlCheck.Ok) "$label · ${check.domain}" else label
}

private val GLYPH = 16.dp
