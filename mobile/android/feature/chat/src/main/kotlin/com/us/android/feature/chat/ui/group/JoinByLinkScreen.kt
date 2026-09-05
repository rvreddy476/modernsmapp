package com.us.android.feature.chat.ui.group

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.InviteLinkState
import com.us.android.core.chat.data.InvitePreview
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.community.ChatFormField
import com.us.android.feature.chat.ui.community.FormError
import com.us.android.feature.chat.ui.home.memberCountLabel

/**
 * Join with a link: a field for the pasted link (skipped when the deep
 * link brought a code), then the preview card — avatar, title, member
 * count, description — and Join. A dead link says so in place of the button.
 */
@Composable
fun JoinByLinkScreen(
    onJoined: (conversationId: String, title: String) -> Unit,
    onBack: () -> Unit,
    viewModel: JoinByLinkViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.joined) {
        val joined = state.joined ?: return@LaunchedEffect
        viewModel.consumeJoined()
        onJoined(joined.conversationId, joined.title)
    }
    UsScaffold(
        topBar = { UsTopBar(title = "Join with link", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            ChatFormField(
                value = state.input,
                onValueChange = viewModel::onInputChange,
                label = "Invite link",
                placeholder = "https://atpost.app/chat/join/…",
                tag = "join_link_input",
            )
            UsSecondaryButton(
                text = "Look up",
                onClick = viewModel::lookUp,
                enabled = state.input.isNotBlank() && !state.loading,
                modifier = Modifier.fillMaxWidth().testTag("join_link_lookup"),
            )
            FormError(state.problem)
            val preview = state.preview
            when {
                state.loading -> UsLoadingState(label = "Looking up the invite", modifier = Modifier.fillMaxWidth())
                preview != null -> PreviewCard(
                    preview = preview,
                    previewState = state.previewState,
                    joining = state.joining,
                    onJoin = viewModel::join,
                    onOpen = viewModel::openExisting,
                )
            }
        }
    }
}

@Composable
private fun PreviewCard(
    preview: InvitePreview,
    previewState: InviteLinkState?,
    joining: Boolean,
    onJoin: () -> Unit,
    onOpen: () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.card)
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCardSolid, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(UsTheme.spacing.xxxxl)
            .testTag("join_link_preview"),
    ) {
        UsAvatar(
            name = preview.title.ifBlank {
                "Group"
            },
            size = UsAvatarSize.Large,
            seed = preview.conversationId,
            imageUrl = preview.avatarUrl
        )
        Text(
            text = preview.title.ifBlank { "Group" },
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = memberCountLabel(preview.memberCount),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        if (preview.description.isNotBlank()) {
            Text(
                text = preview.description,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textBody,
                textAlign = TextAlign.Center,
            )
        }
        when (previewState) {
            InviteLinkState.Live -> UsButton(
                text = if (joining) "Joining…" else "Join group",
                onClick = onJoin,
                enabled = !joining,
                loading = joining,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m).testTag("join_link_join"),
            )
            InviteLinkState.Member -> UsButton(
                text = "Open group",
                onClick = onOpen,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m).testTag("join_link_open"),
            )
            InviteLinkState.Expired, InviteLinkState.NotLive -> DeadLine("This invite link has expired.")
            InviteLinkState.Exhausted -> DeadLine("This invite link has been used up.")
            null -> Unit
        }
    }
}

@Composable
private fun DeadLine(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.error,
        textAlign = TextAlign.Center,
        modifier = Modifier.padding(top = UsTheme.spacing.m).testTag("join_link_dead"),
    )
}

private val HAIRLINE = 1.dp
