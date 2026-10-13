package com.us.android.core.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.launch

/** Callback-only lifecycle adapter: no polling while this screen is hidden. */
@Composable
fun RefreshWhileVisible(events: Flow<String>, intervalMillis: Long = 0, refresh: () -> Unit) {
    val latest = rememberUpdatedState(refresh)
    val owner = LocalLifecycleOwner.current
    LaunchedEffect(owner, events, intervalMillis) {
        owner.lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            latest.value()
            launch { events.collect { latest.value() } }
            if (intervalMillis > 0) launch {
                while (true) { delay(intervalMillis); latest.value() }
            }
        }
    }
}
