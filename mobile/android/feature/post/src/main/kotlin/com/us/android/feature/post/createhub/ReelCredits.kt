package com.us.android.feature.post.createhub

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.designsystem.theme.UsTypeScale
import com.us.android.core.feed.ui.credits.CreditsEditor
import com.us.android.core.feed.ui.credits.CreditsSection

/**
 * The long video's Credits step (video credits, 2026-10-23): cast & crew,
 * genres, year and production house, over the form like "Mention people" —
 * the form's state is the ViewModel's, so a second destination would need to
 * share it. Back and Done both return with everything kept; the step is
 * optional, and Post checks it.
 */
@Composable
internal fun ReelCreditsScreen(editor: CreditsEditor, onOpen: () -> Unit, onDone: () -> Unit, enabled: Boolean) {
    BackHandler(onBack = onDone)
    LaunchedEffect(Unit) { onOpen() }
    val state by editor.state.collectAsStateWithLifecycle()
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .imePadding()
            .testTag("reel-credits"),
    ) {
        CreditsHeader(count = state.form.filled.size, onDone = onDone)
        Column(
            modifier = Modifier
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            Spacer(Modifier.height(UsTheme.spacing.s))
            CreditsSection(state = state, editor = editor, enabled = enabled)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
        }
    }
}

/** Back, the title, and Done with the running count — the "Mention people" header. */
@Composable
private fun CreditsHeader(count: Int, onDone: () -> Unit) {
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
            text = "Credits",
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
                .testTag("reel-credits-done"),
        )
    }
}

private val TAP_TARGET = 40.dp
private val TITLE_SIZE = UsTypeScale.lg.size
