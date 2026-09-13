package com.us.android.core.realtime

import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.lifecycleScope
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.FlowCollector
import kotlinx.coroutines.launch

/**
 * Collects [this] only while [owner] is at least STARTED.
 *
 * A realtime subscription is a held socket and a server-side connection slot
 * (notification-service caps them per user). Going to STOPPED cancels the
 * collection, which cancels the stream; returning to STARTED opens a new one,
 * and the SSE client's Last-Event-ID resume is per subscription, so screens
 * that need gap-fill across a background period should re-read their state
 * on resume rather than rely on the stream.
 *
 * The returned job ends when [owner] is destroyed.
 */
fun <T> Flow<T>.collectWhileStarted(owner: LifecycleOwner, collector: FlowCollector<T>): Job =
    owner.lifecycleScope.launch {
        owner.repeatOnLifecycle(Lifecycle.State.STARTED) {
            collect(collector)
        }
    }
