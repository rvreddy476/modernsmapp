package com.us.android.screentime

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

/**
 * Renders the daily-limit / sleep-hours nudge over whatever is on screen. A
 * dialog rather than a full destination: it must interrupt nothing about
 * navigation state, and "OK" is the only thing it can do — there is no
 * enforcement behind it.
 */
@Composable
fun ScreenTimeGuardHost(viewModel: ScreenTimeGuardViewModel = hiltViewModel()) {
    val message by viewModel.message.collectAsStateWithLifecycle()
    message?.let { current ->
        AlertDialog(
            onDismissRequest = viewModel::dismiss,
            title = { Text(current.title()) },
            text = { Text(current.body()) },
            confirmButton = {
                TextButton(onClick = viewModel::dismiss) { Text("OK") }
            },
        )
    }
}

private fun ScreenTimeGuardMessage.title(): String = when (this) {
    ScreenTimeGuardMessage.DAILY_LIMIT -> "You've reached your daily limit"
    ScreenTimeGuardMessage.SLEEP_TIME -> "It's your sleep time"
}

private fun ScreenTimeGuardMessage.body(): String = when (this) {
    ScreenTimeGuardMessage.DAILY_LIMIT ->
        "You've used as much time today as you asked us to allow. You can keep going, or take a break."
    ScreenTimeGuardMessage.SLEEP_TIME ->
        "This is inside the sleep hours you set. You can keep going, or put the phone down."
}
