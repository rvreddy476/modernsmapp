package com.us.android.core.feed.offline

import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsOfflineAction
import com.us.android.core.ui.UsOfflineMoreState

/**
 * What a video's More sheet offers about an offline copy (2026-10-02),
 * from the post and where its copy stands on this device.
 *
 * What is on the device comes first: a stored copy can always be removed
 * and a save in flight can always be cancelled, even after the creator
 * switched saving off (the next check removes the copy anyway; until then
 * the viewer must not be left holding one they cannot let go of). With
 * nothing on the device, Save offline is offered only where the server
 * would grant it ([canSaveOffline]): the creator allows it, or the video is
 * the viewer's own.
 *
 * A reel that plays an added sound is offered like any other: its sound is
 * stored beside it and mixed by the same player as online.
 */
fun offlineMoreState(item: FeedItem, isOwn: Boolean, entry: OfflineEntry?): UsOfflineMoreState = when (entry?.phase) {
    OfflinePhase.STORED -> UsOfflineMoreState(UsOfflineAction.REMOVE)
    OfflinePhase.WAITING ->
        UsOfflineMoreState(UsOfflineAction.CANCEL, progress = entry.progress, waiting = WAITING_FOR_NETWORK)
    OfflinePhase.REQUESTING, OfflinePhase.SAVING ->
        UsOfflineMoreState(UsOfflineAction.CANCEL, progress = entry.progress)
    null -> UsOfflineMoreState(if (item.canSaveOffline(isOwn)) UsOfflineAction.SAVE else UsOfflineAction.NONE)
}

/** What a held save says, on the row and on the ring. Wi-Fi only is the default, so this is the usual reason. */
const val WAITING_FOR_NETWORK = "Waiting for Wi-Fi"

/** What Save offline says once the copy is granted and held: the viewer's own switch, named so they can change it. */
const val SAVES_ON_WIFI = "This will save when you're on Wi-Fi. You can change that on the Offline page."
