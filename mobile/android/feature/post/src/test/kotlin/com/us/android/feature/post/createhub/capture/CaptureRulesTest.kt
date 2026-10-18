package com.us.android.feature.post.createhub.capture

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.post.createhub.CreateSurface
import org.junit.Test

/** The capture-first create screen's rules (2026-10-18). */
class CaptureRulesTest {

    private fun photo(n: Int, album: String? = "Camera") = PickerItem(uri = "content://p/$n", isVideo = false, album = album)
    private fun video(n: Int, album: String? = "Camera") =
        PickerItem(uri = "content://v/$n", isVideo = true, durationMs = 12_000, album = album)

    @Test
    fun `only photo is shot here, every other mode hands over to its own composer`() {
        assertThat(CaptureMode.entries.map { it.handsOverTo }).containsExactly(
            CreateSurface.Text, null, CreateSurface.Reel, CreateSurface.Reel, CreateSurface.Video,
        ).inOrder()
        assertThat(DEFAULT_CAPTURE_MODE).isEqualTo(CaptureMode.PHOTO)
    }

    @Test
    fun `photos number in the order they are picked and close up when one is let go`() {
        var picked = emptyList<PickerItem>()
        picked = toggleSelection(picked, photo(1))
        picked = toggleSelection(picked, photo(2))
        picked = toggleSelection(picked, photo(3))
        assertThat(selectionNumber(picked, photo(3))).isEqualTo(3)
        picked = toggleSelection(picked, photo(1))
        assertThat(selectionNumber(picked, photo(3))).isEqualTo(2)
        assertThat(selectionNumber(picked, photo(1))).isNull()
    }

    @Test
    fun `ten photos at most, and the eleventh changes nothing`() {
        val ten = (1..10).fold(emptyList<PickerItem>()) { acc, n -> toggleSelection(acc, photo(n)) }
        assertThat(ten).hasSize(MAX_PICKED_PHOTOS)
        assertThat(toggleSelection(ten, photo(11))).isEqualTo(ten)
    }

    @Test
    fun `a video posts alone`() {
        val photos = listOf(photo(1), photo(2))
        assertThat(toggleSelection(photos, video(1))).containsExactly(video(1))
        assertThat(toggleSelection(listOf(video(1)), photo(3))).containsExactly(photo(3))
        assertThat(toggleSelection(listOf(video(1)), video(2))).containsExactly(video(2))
    }

    @Test
    fun `next goes to the studio with photos, to the reel flow with a video`() {
        assertThat(pickerResult(emptyList())).isNull()
        assertThat(pickerResult(listOf(photo(1), photo(2)))).isEqualTo(PickerResult.Photos(listOf("content://p/1", "content://p/2")))
        assertThat(pickerResult(listOf(video(4)))).isEqualTo(PickerResult.Video("content://v/4"))
        assertThat(nextLabel(0)).isEqualTo("Next")
        assertThat(nextLabel(3)).isEqualTo("Next (3)")
    }

    @Test
    fun `tabs and albums narrow the grid`() {
        val all = listOf(photo(1, "Camera"), video(1, "Camera"), photo(2, "Screenshots"), photo(3, null))
        assertThat(pickerItems(all, PickerTab.ALL, ALL_ALBUMS)).hasSize(4)
        assertThat(pickerItems(all, PickerTab.VIDEOS, ALL_ALBUMS)).containsExactly(video(1, "Camera"))
        assertThat(pickerItems(all, PickerTab.PHOTOS, "Screenshots")).containsExactly(photo(2, "Screenshots"))
        assertThat(pickerAlbums(all)).containsExactly(ALL_ALBUMS, "Camera", "Screenshots").inOrder()
    }
}
