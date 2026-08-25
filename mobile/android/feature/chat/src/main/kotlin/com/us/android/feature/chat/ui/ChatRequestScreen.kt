package com.us.android.feature.chat.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState

/**
 * A message request, before any decision (directive §3.3, §5.5).
 *
 * The sender's single introduction renders read-only — no composer, no
 * receipts, no presence. Four decisions, all idempotent server transitions:
 * Accept opens the thread; Decline quietly parks it (with a server-side
 * re-request cooldown); Block severs both sides; Report severs and files the
 * stored preview with trust & safety.
 */
@Composable
@Suppress("LongMethod")
fun ChatRequestScreen(
    title: String,
    onAccepted: (conversationId: String, title: String) -> Unit,
    onClosed: () -> Unit,
    onBack: () -> Unit,
    viewModel: ChatRequestViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    androidx.compose.runtime.LaunchedEffect(state.outcome) {
        when (state.outcome) {
            RequestOutcome.Accepted -> onAccepted(viewModel.conversationId, title)
            RequestOutcome.Closed -> onClosed()
            null -> Unit
        }
    }

    UsScaffold(
        topBar = { UsTopBar(title = "Message request", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            if (state.loading) {
                UsLoadingState(label = "Loading request")
                return@Column
            }

            Spacer(Modifier.height(UsTheme.spacing.xl))
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                UsAvatar(name = title, size = UsAvatarSize.Large, seed = viewModel.conversationId)
                Column {
                    Text(
                        title,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.textPrimary,
                    )
                    Text(
                        "Wants to send you messages",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }

            Spacer(Modifier.height(UsTheme.spacing.l))

            // The one permitted introduction, quoted rather than threaded —
            // this is a decision screen, not a conversation.
            state.preview?.let { preview ->
                Text(
                    text = "“$preview”",
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.testTag("request-preview"),
                )
            }

            Text(
                "They won't see whether you've read this, or when you're online, unless you accept.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(top = UsTheme.spacing.l),
            )

            Spacer(Modifier.weight(1f))

            state.error?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                Spacer(Modifier.height(UsTheme.spacing.s))
            }

            UsButton(
                text = if (state.busy) "Working…" else "Accept",
                onClick = viewModel::accept,
                enabled = !state.busy,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("request-accept"),
            )
            Spacer(Modifier.height(UsTheme.spacing.s))
            UsSecondaryButton(
                text = "Decline",
                onClick = viewModel::decline,
                enabled = !state.busy,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("request-decline"),
            )
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceEvenly,
            ) {
                TextButton(
                    onClick = viewModel::block,
                    enabled = !state.busy,
                    modifier = Modifier.testTag("request-block"),
                ) {
                    Text("Block", color = MaterialTheme.colorScheme.error)
                }
                TextButton(
                    onClick = viewModel::report,
                    enabled = !state.busy,
                    modifier = Modifier.testTag("request-report"),
                ) {
                    Text("Report", color = MaterialTheme.colorScheme.error)
                }
            }
            Spacer(Modifier.height(UsTheme.spacing.l))
        }
    }
}
