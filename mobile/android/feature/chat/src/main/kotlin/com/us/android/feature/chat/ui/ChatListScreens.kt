package com.us.android.feature.chat.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.ui.UsEmptyState

/**
 * Pending group invitations, each with Join / Decline — the ≡ menu's
 * "Group invites" (2026-09-05). The rows and the accept/decline calls are
 * the inbox's own; only the frame is new.
 */
@Composable
fun InvitationsScreen(
    onBack: () -> Unit,
    viewModel: ChatInboxViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Group invites", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            if (state.invitations.isEmpty()) {
                UsEmptyState(
                    title = "No group invites",
                    detail = "Groups that need your consent to join appear here.",
                )
                return@Column
            }
            LazyColumn(modifier = Modifier.fillMaxSize().testTag("chat_invitations")) {
                items(state.invitations, key = { it.id }) { invitation ->
                    InvitationRow(
                        busy = invitation.id in state.busyInvitationIds,
                        onAccept = { viewModel.acceptInvitation(invitation.id) },
                        onDecline = { viewModel.declineInvitation(invitation.id) },
                    )
                }
            }
        }
    }
}

/** Message requests — the Chats tab's door (2026-09-05). Tapping one opens its decision screen. */
@Composable
fun RequestsListScreen(
    onOpenRequest: (conversationId: String, title: String) -> Unit,
    onBack: () -> Unit,
    viewModel: ChatInboxViewModel = hiltViewModel(),
    filtered: FilteredRequestsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // Chat parity (CONTRACT §7): safe mode's filtered sub-list, a second tab
    // when the server has one; the plain list stays exactly as it was.
    val filteredRows by filtered.rows.collectAsStateWithLifecycle()
    var showFiltered by androidx.compose.runtime.saveable.rememberSaveable { androidx.compose.runtime.mutableStateOf(false) }
    UsScaffold(
        topBar = { UsTopBar(title = "Message requests", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            val filteredList = filteredRows
            if (filteredList != null) {
                RequestFilterTabs(
                    filteredCount = filteredList.size,
                    showingFiltered = showFiltered,
                    onSelect = { showFiltered = it },
                )
            }
            if (showFiltered && filteredList != null) {
                if (filteredList.isEmpty()) {
                    UsEmptyState(
                        title = "Nothing filtered",
                        detail = "Requests that look risky — brand-new accounts, links, words you filter — wait here.",
                    )
                } else {
                    LazyColumn(modifier = Modifier.fillMaxSize().testTag("chat_requests_filtered_list")) {
                        items(filteredList, key = { it.id }) { request ->
                            val title = request.displayTitle(state.viewerId)
                            ConversationRow(
                                conversation = request,
                                title = title,
                                viewerId = state.viewerId,
                                onClick = { onOpenRequest(request.id, title) },
                            )
                        }
                    }
                }
                return@Column
            }
            if (state.requests.isEmpty()) {
                UsEmptyState(
                    title = "No message requests",
                    detail = "When someone outside your connections messages you, it waits here for your decision.",
                )
                return@Column
            }
            LazyColumn(modifier = Modifier.fillMaxSize().testTag("chat_requests")) {
                items(state.requests, key = { it.id }) { request ->
                    val title = request.displayTitle(state.viewerId)
                    ConversationRow(
                        conversation = request,
                        title = title,
                        viewerId = state.viewerId,
                        onClick = { onOpenRequest(request.id, title) },
                    )
                }
            }
        }
    }
}
