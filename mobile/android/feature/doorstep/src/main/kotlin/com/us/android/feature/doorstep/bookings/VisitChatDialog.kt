package com.us.android.feature.doorstep.bookings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.data.TrustedContactDto

@Composable
internal fun VisitChatDialog(state: BookingDetailUiState, onDismiss: () -> Unit, onSend: (String, () -> Unit) -> Unit, onMore: () -> Unit) {
    var draft by rememberSaveable { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Visit conversation") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text("Available from acceptance until two hours after completion.", style = MaterialTheme.typography.bodySmall)
                LazyColumn(Modifier.fillMaxWidth().heightIn(max = 280.dp), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    items(state.conversation?.items.orEmpty(), key = { it.id }) { message ->
                        Column {
                            Text(if (message.senderKind == "customer") "You" else "Professional", style = MaterialTheme.typography.labelSmall)
                            Text(message.body, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                    if (state.conversation?.nextCursor != null) item { TextButton(onClick = onMore, enabled = !state.chatLoading) { Text("Load more") } }
                }
                if (state.chatLoading) Text("Updating…", style = MaterialTheme.typography.bodySmall)
                state.chatError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                if (state.conversation?.open == true) {
                    UsTextField(value = draft, onValueChange = { draft = it }, label = "Message", errorText = "Use up to 1,000 characters".takeIf { draft.length > 1000 }, singleLine = false)
                    Text("${draft.length}/1,000", style = MaterialTheme.typography.labelSmall)
                }
                else if (state.conversation != null) Text("This conversation is closed.")
            }
        },
        confirmButton = { TextButton(enabled = state.conversation?.open == true && !state.chatBusy && draft.isNotBlank() && draft.length <= 1000, onClick = { onSend(draft) { draft = "" } }) { Text(if (state.chatBusy) "Sending…" else "Send") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Close") } },
    )
}

@Composable
internal fun TrustedContactDialog(contact: TrustedContactDto?, busy: Boolean, error: String?, onDismiss: () -> Unit, onSave: (String, String) -> Unit) {
    var name by rememberSaveable { mutableStateOf("") }
    var phone by rememberSaveable { mutableStateOf("") }
    LaunchedEffect(contact?.updatedAt) { if (name.isBlank()) name = contact?.name.orEmpty() }
    val valid = name.isNotBlank() && name.length <= 80 && Regex("^\\+91[6-9][0-9]{9}$").matches(phone)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Trusted contact") },
        text = { Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            contact?.let { Text("Saved: ${it.name} · ${it.phoneMasked}") }
            Text("A contact for visit safety. For an emergency, call 112 directly.", style = MaterialTheme.typography.bodySmall)
            UsTextField(value = name, onValueChange = { name = it }, label = "Name", errorText = "Use up to 80 characters".takeIf { name.length > 80 })
            UsTextField(value = phone, onValueChange = { phone = it }, label = "Phone", errorText = "Use +91 followed by the ten-digit number.".takeIf { phone.isNotEmpty() && !Regex("^\\+91[6-9][0-9]{9}$").matches(phone) }, keyboardType = androidx.compose.ui.text.input.KeyboardType.Phone)
        } },
        confirmButton = { TextButton(enabled = valid && !busy, onClick = { onSave(name, phone) }) { Text(if (busy) "Saving…" else "Save") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
internal fun SupportDialog(state: BookingDetailUiState, onDismiss: () -> Unit, onSend: (String, String, String, () -> Unit) -> Unit) {
    var category by rememberSaveable { mutableStateOf("quality") }
    var subject by rememberSaveable { mutableStateOf("") }
    var body by rememberSaveable { mutableStateOf("") }
    val valid = subject.isNotBlank() && subject.length <= 160 && body.isNotBlank() && body.length <= 4000
    AlertDialog(onDismissRequest = onDismiss, title = { Text("Visit support") }, text = {
        LazyColumn(Modifier.fillMaxWidth().heightIn(max = 360.dp), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            item { Text("For an emergency, call 112. Do not wait for support.", style = MaterialTheme.typography.bodySmall) }
            items(state.tickets, key = { it.id }) { ticket -> Column { Text("${ticket.subject} · ${ticket.status.replace('_', ' ')}", style = MaterialTheme.typography.labelLarge); Text(ticket.body) } }
            item {
                androidx.compose.foundation.lazy.LazyRow {
                    items(listOf("quality", "payment", "safety", "damage", "professional", "other")) { topic ->
                        androidx.compose.material3.FilterChip(selected = category == topic, onClick = { category = topic }, label = { Text(topic) })
                    }
                }
            }
            item { UsTextField(value = subject, onValueChange = { subject = it }, label = "Subject (required)", errorText = "Use up to 160 characters".takeIf { subject.length > 160 }) }
            item { UsTextField(value = body, onValueChange = { body = it }, label = "What happened? (required)", singleLine = false, errorText = "Use up to 4,000 characters".takeIf { body.length > 4000 }) }
            state.ticketError?.let { message -> item { Text(message, color = MaterialTheme.colorScheme.error) } }
        }
    }, confirmButton = { TextButton(enabled = valid && !state.ticketBusy, onClick = { onSend(category, subject, body) { subject = ""; body = ""; onDismiss() } }) { Text(if (state.ticketBusy) "Sending…" else "Send request") } }, dismissButton = { TextButton(onClick = onDismiss) { Text("Close") } })
}
