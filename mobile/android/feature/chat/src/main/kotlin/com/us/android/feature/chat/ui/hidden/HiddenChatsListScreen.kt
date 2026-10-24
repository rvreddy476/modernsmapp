package com.us.android.feature.chat.ui.hidden

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.hidden.CHAT_PASSWORD_MAX
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState

/**
 * The hidden list, behind the secret key: every hidden chat with a lock
 * glyph. A row opens with the chat password — or fingerprint/face once the
 * password was entered here — and stays open for this unlock session. The
 * guard around this screen (navigation) holds FLAG_SECURE and leaves the
 * moment the list re-locks.
 */
@Composable
fun HiddenChatsListScreen(
    onBack: () -> Unit,
    onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    onForgot: () -> Unit,
    viewModel: HiddenChatsListViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.openThread) {
        val row = state.openThread ?: return@LaunchedEffect
        viewModel.consumeOpen()
        onOpenThread(row.id, row.displayTitle(state.viewerId), row.type == GROUP)
    }
    UsScaffold(
        topBar = {
            UsTopBar(
                title = "Hidden chats",
                onBack = onBack,
                actions = {
                    IconButton(onClick = viewModel::lockNow, modifier = Modifier.testTag("hidden-lock-now")) {
                        Icon(UsIcons.Lock, contentDescription = "Lock hidden chats", tint = UsTheme.extended.textPrimary)
                    }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        val rows = state.rows
        when {
            rows == null && state.loading -> UsLoadingState(Modifier.padding(padding), "Loading hidden chats")
            rows.isNullOrEmpty() -> UsEmptyState(
                title = "No hidden chats",
                modifier = Modifier.padding(padding),
                detail = "Long-press a chat in Messages and choose Hide chat.",
            )
            else -> LazyColumn(
                modifier = Modifier.padding(padding).fillMaxSize().testTag("hidden-list"),
            ) {
                items(rows, key = { it.id }) { row ->
                    HiddenRow(row, row.displayTitle(state.viewerId)) { viewModel.select(row) }
                }
            }
        }
    }
    state.prompt?.let { prompt ->
        OpenPromptSheet(
            prompt = prompt,
            title = prompt.conversation.displayTitle(state.viewerId),
            biometricReady = state.biometricReady,
            biometricOffer = state.biometricOffer,
            onSubmit = viewModel::submitPassword,
            onBiometric = viewModel::openWithBiometric,
            onForgot = {
                viewModel.dismissPrompt()
                onForgot()
            },
            onDismiss = viewModel::dismissPrompt,
        )
    }
}

@Composable
private fun HiddenRow(conversation: Conversation, title: String, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("hidden-row:${conversation.id}"),
    ) {
        UsAvatar(
            name = title,
            size = UsAvatarSize.Chat,
            seed = conversation.id,
            imageUrl = conversation.peer?.avatarUrl ?: conversation.avatarUrl,
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = "Hidden chat",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Icon(
            imageVector = UsIcons.Lock,
            contentDescription = "Hidden",
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(LOCK_GLYPH).testTag("hidden-row-lock"),
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
@Suppress("LongParameterList", "LongMethod")
private fun OpenPromptSheet(
    prompt: OpenPrompt,
    title: String,
    biometricReady: Boolean,
    biometricOffer: Boolean,
    onSubmit: (password: String, remember: Boolean, activity: FragmentActivity?) -> Unit,
    onBiometric: (FragmentActivity) -> Unit,
    onForgot: () -> Unit,
    onDismiss: () -> Unit,
) {
    // remember, NOT rememberSaveable: a secret never goes into saved state.
    var password by remember(prompt.conversation.id) { mutableStateOf("") }
    var rememberBiometric by remember { mutableStateOf(true) }
    val activity = LocalContext.current as? FragmentActivity
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.xl)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("hidden-open-sheet"),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text("Chat password", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "Enter your chat password to open $title.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            UsTextField(
                value = password,
                onValueChange = { password = it.take(CHAT_PASSWORD_MAX * 2) },
                label = "Chat password",
                isPassword = true,
                errorText = prompt.error,
                modifier = Modifier.fillMaxWidth().testTag("hidden-open-password"),
            )
            if (biometricOffer) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.clickable { rememberBiometric = !rememberBiometric },
                ) {
                    Checkbox(checked = rememberBiometric, onCheckedChange = { rememberBiometric = it })
                    Text("Use fingerprint or face next time", color = UsTheme.extended.textPrimary)
                }
            }
            UsButton(
                text = "Open chat",
                onClick = {
                    onSubmit(password, rememberBiometric, activity)
                    password = ""
                },
                enabled = password.isNotEmpty() && !prompt.blocked,
                loading = prompt.busy,
                modifier = Modifier.fillMaxWidth().testTag("hidden-open-submit"),
            )
            if (biometricReady && activity != null) {
                UsSecondaryButton(
                    text = "Use fingerprint or face",
                    onClick = { onBiometric(activity) },
                    enabled = !prompt.blocked && !prompt.busy,
                    modifier = Modifier.fillMaxWidth().testTag("hidden-open-biometric"),
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.xs))
            TextButton(onClick = onForgot, modifier = Modifier.testTag("hidden-open-forgot")) {
                Text("Forgot your chat password?")
            }
        }
    }
}

private const val GROUP = "group"
private val LOCK_GLYPH = 18.dp
