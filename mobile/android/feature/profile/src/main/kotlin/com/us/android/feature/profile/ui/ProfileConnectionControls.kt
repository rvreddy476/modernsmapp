package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.ConnectionStatus

/** What the profile's connection control draws (2026-10-24). */
enum class ConnectionControl {
    /** Send a request: only while [CONNECT_OFFERED]. */
    CONNECT,

    /** The viewer asked; a tap withdraws it. */
    REQUEST_SENT,

    /** This account asked the viewer: Accept and Decline. */
    RESPOND,

    /** Connected; a tap asks "Remove connection?". */
    CONNECTED,
}

/**
 * The control for [status], or none. Pure, so the table is a test: nothing
 * while there is no connection and sending is not offered — a Connect button
 * the product no longer has would be a dead end.
 */
fun connectionControlFor(status: ConnectionStatus, offered: Boolean = CONNECT_OFFERED): ConnectionControl? = when (status) {
    ConnectionStatus.NONE -> if (offered) ConnectionControl.CONNECT else null
    ConnectionStatus.PENDING_SENT -> ConnectionControl.REQUEST_SENT
    ConnectionStatus.PENDING_RECEIVED -> ConnectionControl.RESPOND
    ConnectionStatus.CONNECTED -> ConnectionControl.CONNECTED
}

/** The connection control under Follow, on someone else's profile. */
@Composable
internal fun ConnectionControls(
    name: String,
    status: ConnectionStatus,
    busy: Boolean,
    onTap: () -> Unit,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
) {
    when (connectionControlFor(status)) {
        null -> Unit
        ConnectionControl.CONNECT -> UsSecondaryButton(
            text = "Connect",
            onClick = onTap,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth().testTag("profile_connect"),
        )
        ConnectionControl.REQUEST_SENT -> UsSecondaryButton(
            text = "Connection requested · Cancel",
            onClick = onTap,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth().testTag("profile_connect_sent"),
        )
        ConnectionControl.CONNECTED -> UsSecondaryButton(
            text = "Connected",
            onClick = onTap,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth().testTag("profile_connected"),
        )
        ConnectionControl.RESPOND -> Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            modifier = Modifier.fillMaxWidth().testTag("profile_connect_respond"),
        ) {
            Text(
                text = "${name.ifBlank { "This account" }} wants to connect with you",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsButton(text = "Accept", onClick = onAccept, loading = busy, modifier = Modifier.weight(1f))
                UsSecondaryButton(text = "Decline", onClick = onDecline, enabled = !busy, modifier = Modifier.weight(1f))
            }
        }
    }
}

/** "Remove connection?" — removing one confirms first, as cancelling a follow request does. */
@Composable
internal fun RemoveConnectionDialog(name: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Remove ${name.ifBlank { "this person" }} from your connections?") },
        text = { Text("They won't be told. Following stays as it is.") },
        confirmButton = { TextButton(onClick = onConfirm) { Text("Remove") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
