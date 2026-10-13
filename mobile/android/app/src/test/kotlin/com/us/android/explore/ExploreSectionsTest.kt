package com.us.android.explore

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class ExploreSectionsTest {
    @Test
    fun `every available and coming soon app appears exactly once`() {
        val tiles = launcherTiles()
        val grouped = exploreSections(tiles, "").flatMap { it.tiles }
        assertThat(grouped).containsExactlyElementsIn(tiles)
        assertThat(grouped.map { it.app }.distinct()).hasSize(tiles.size)
    }

    @Test
    fun `search trims and matches names and service descriptions without losing availability`() {
        val ride = exploreSections(launcherTiles(), " RIDE ").flatMap { it.tiles }
        assertThat(ride.map { it.app }).containsExactly(LauncherApp.RIDE)
        val shop = exploreSections(launcherTiles(), "shopping").flatMap { it.tiles }
        assertThat(shop.map { it.app }).containsExactly(LauncherApp.MSTORE)
        val unavailable = listOf(LauncherTile(LauncherApp.ASK, true))
        assertThat(exploreSections(unavailable, "questions").single().tiles).isEqualTo(unavailable)
    }

    @Test
    fun `no matches returns no empty section headings`() {
        assertThat(exploreSections(launcherTiles(), "not-a-real-service")).isEmpty()
    }
}
