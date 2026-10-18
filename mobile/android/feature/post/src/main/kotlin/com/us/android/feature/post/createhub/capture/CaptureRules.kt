package com.us.android.feature.post.createhub.capture

import com.us.android.feature.post.createhub.CreateSurface

/*
 * The capture-first create screen's rules (post-screens parity, 2026-10-18),
 * pure so each is a table test: what each mode chip hands over to, and how
 * the gallery picker selects.
 */

/**
 * The mode chips under the shutter, left to right. Only [PHOTO] is shot on
 * this screen; the others hand over to the composer that already makes them
 * — the text composer, the reel flow with its own camera and editor, the
 * long-video form — rather than a second recorder here.
 */
enum class CaptureMode(val label: String, val handsOverTo: CreateSurface?) {
    TEXT("Text", CreateSurface.Text),
    PHOTO("Photo", null),
    REEL_SHORT("Reel 15s", CreateSurface.Reel),
    REEL_LONG("Reel 60s", CreateSurface.Reel),
    VIDEO("Video 10m", CreateSurface.Video),
}

/** The screen opens on Photo: the shutter takes a picture. */
val DEFAULT_CAPTURE_MODE = CaptureMode.PHOTO

/** The picker's tabs. */
enum class PickerTab(val label: String) { ALL("All"), VIDEOS("Videos"), PHOTOS("Photos") }

/** One item of the device's media, as the picker lists it. */
data class PickerItem(
    val uri: String,
    val isVideo: Boolean,
    val durationMs: Long? = null,
    /** The album (MediaStore bucket) it is in; null when the store does not say. */
    val album: String? = null,
)

/** "Recents" is every album; anything else is the album's own name. */
const val ALL_ALBUMS = "Recents"

/** The items a tab and an album show, in the order given (newest first). */
fun pickerItems(items: List<PickerItem>, tab: PickerTab, album: String): List<PickerItem> = items.filter { item ->
    val kindMatches = when (tab) {
        PickerTab.ALL -> true
        PickerTab.VIDEOS -> item.isVideo
        PickerTab.PHOTOS -> !item.isVideo
    }
    kindMatches && (album == ALL_ALBUMS || item.album == album)
}

/** The album switcher's list: Recents, then every album in the order it first appears. */
fun pickerAlbums(items: List<PickerItem>): List<String> =
    listOf(ALL_ALBUMS) + items.mapNotNull { it.album?.takeIf { name -> name.isNotBlank() } }.distinct()

/**
 * A tap on [item]: a selected item is let go (the numbers after it close
 * up); a video posts on its own, so it replaces whatever was picked, and a
 * photo after a video starts over; photos add up to [max] (the studio's
 * ten) and a tap past that changes nothing.
 */
fun toggleSelection(selected: List<PickerItem>, item: PickerItem, max: Int = MAX_PICKED_PHOTOS): List<PickerItem> = when {
    selected.any { it.uri == item.uri } -> selected.filterNot { it.uri == item.uri }
    item.isVideo -> listOf(item)
    selected.any { it.isVideo } -> listOf(item)
    selected.size >= max -> selected
    else -> selected + item
}

/** The number in an item's circle: 1-based, or null when it is not picked. */
fun selectionNumber(selected: List<PickerItem>, item: PickerItem): Int? =
    selected.indexOfFirst { it.uri == item.uri }.takeIf { it >= 0 }?.plus(1)

/** "Next", or "Next (3)" once something is picked. */
fun nextLabel(count: Int): String = if (count <= 0) "Next" else "Next ($count)"

/** Where Next goes with what is picked. */
sealed interface PickerResult {
    /** One to ten photos: the photo studio. */
    data class Photos(val uris: List<String>) : PickerResult

    /** One video: the reel flow, starting from it. */
    data class Video(val uri: String) : PickerResult
}

/** What Next does; null when nothing is picked. */
fun pickerResult(selected: List<PickerItem>): PickerResult? = when {
    selected.isEmpty() -> null
    selected.first().isVideo -> PickerResult.Video(selected.first().uri)
    else -> PickerResult.Photos(selected.filterNot { it.isVideo }.map { it.uri })
}

/** The studio takes up to ten photos. */
const val MAX_PICKED_PHOTOS = 10
