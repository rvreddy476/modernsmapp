package com.us.android.feature.live.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.live.data.LiveGate
import com.us.android.feature.live.data.LiveGateAction
import com.us.android.feature.live.data.LiveProgressDto
import com.us.android.feature.live.data.LiveRequirementDto
import com.us.android.feature.live.data.RequirementState

/**
 * "Not yet": what going live still needs (founder, 2026-10-02, after
 * YouTube's "Almost there" — the idea, in our own words and our own glyphs).
 *
 * A short heading, each requirement with a tick or what is still needed in
 * plain words, ONE primary button that helps the first requirement the user
 * can do something about, and "Learn more", which unfolds a short
 * explanation in place. [busy] is the server being asked again: the list
 * stays up and the button spins.
 */
@Composable
internal fun LiveNotYetView(
    gate: LiveGate.NotYet,
    busy: Boolean,
    onAction: (LiveGateAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    var learnMoreOpen by rememberSaveable { mutableStateOf(false) }
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xxl)
            .testTag("live-not-yet"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(HERO_TILE)
                .background(UsTheme.extended.bgRaised, CircleShape),
        ) {
            Icon(
                UsIcons.Live,
                contentDescription = null,
                tint = UsTheme.extended.onMedia,
                modifier = Modifier.size(HERO_GLYPH),
            )
        }
        Spacer(Modifier.height(UsTheme.spacing.xl))
        Text(
            LIVE_GATE_TITLE,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.onMedia,
            textAlign = TextAlign.Center,
            modifier = Modifier.semantics { heading() },
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            LIVE_GATE_DETAIL,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMediaMuted,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(UsTheme.spacing.xxl))
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier.fillMaxWidth(),
        ) {
            requirementLines(gate.requirements).forEach { line -> RequirementRow(line) }
        }
        Spacer(Modifier.height(UsTheme.spacing.xxl))
        UsButton(
            text = gateActionLabel(gate.action),
            onClick = { onAction(gate.action) },
            loading = busy,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-not-yet-action"),
        )
        Spacer(Modifier.height(UsTheme.spacing.m))
        UsSecondaryButton(
            text = if (learnMoreOpen) "Show less" else "Learn more",
            onClick = { learnMoreOpen = !learnMoreOpen },
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-not-yet-learn-more"),
        )
        AnimatedVisibility(visible = learnMoreOpen) {
            Text(
                LIVE_GATE_LEARN_MORE,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.onMediaMuted,
                modifier = Modifier
                    .padding(top = UsTheme.spacing.l)
                    .testTag("live-not-yet-explanation"),
            )
        }
    }
}

/** A tick when met, an open circle when not, a question mark when the server could not check. */
@Composable
private fun RequirementRow(line: RequirementLine) {
    val (icon, tint, said) = requirementGlyph(line.state)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .semantics(mergeDescendants = true) { contentDescription = "$said: ${line.text}" }
            .testTag("live-requirement:${line.key}"),
    ) {
        Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(ROW_GLYPH))
        Text(
            line.text,
            style = MaterialTheme.typography.bodyMedium,
            color = if (line.state == RequirementState.Met) UsTheme.extended.onMediaMuted else UsTheme.extended.onMedia,
            modifier = Modifier.weight(1f),
        )
    }
}

@Composable
private fun requirementGlyph(state: RequirementState): Triple<ImageVector, Color, String> = when (state) {
    RequirementState.Met -> Triple(UsIcons.Check, UsTheme.extended.statusSuccess, "Done")
    RequirementState.Unmet -> Triple(UsIcons.Circle, UsTheme.extended.onMedia, "Still needed")
    RequirementState.Unknown -> Triple(UsIcons.CircleHelp, UsTheme.extended.statusWarning, "Not checked")
}

private val HERO_TILE = 72.dp
private val HERO_GLYPH = 32.dp
private val ROW_GLYPH = 20.dp

@Preview
@Composable
private fun LiveNotYetViewPreview() {
    UsTheme(darkTheme = true) {
        Box(Modifier.background(MaterialTheme.colorScheme.scrim)) {
            LiveNotYetView(
                gate = LiveGate.NotYet(
                    requirements = listOf(
                        LiveRequirementDto(key = "phone_verified", met = true),
                        LiveRequirementDto(key = "adult", met = null),
                        LiveRequirementDto(key = "account_age", met = false, current = 2, needed = 7, unit = "days"),
                        LiveRequirementDto(
                            key = "activity",
                            met = false,
                            posts = LiveProgressDto(current = 1, needed = 3),
                            followers = LiveProgressDto(current = 4, needed = 10),
                        ),
                        LiveRequirementDto(key = "good_standing", met = true),
                    ),
                    action = LiveGateAction.CreatePost,
                ),
                busy = false,
                onAction = {},
            )
        }
    }
}
