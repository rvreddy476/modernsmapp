package com.us.android.feature.tube.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import com.us.android.core.model.FeedItem

/**
 * Which row's "more" sheet is up, and whether that row reads as a
 * suggestion. One holder per page so every ⋮ on it — hero, card, short —
 * opens the same sheet through the same line.
 */
@Stable
class TubeMoreState {
    var item by mutableStateOf<FeedItem?>(null)
        private set
    var suggested by mutableStateOf(true)
        private set

    fun open(item: FeedItem, suggested: Boolean) {
        this.suggested = suggested
        this.item = item
    }

    fun close() {
        item = null
    }
}

@Composable
fun rememberTubeMoreState(): TubeMoreState = remember { TubeMoreState() }
