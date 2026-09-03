package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.post.navigation.CreateRoute
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Sheet tile → route, and the route's own bytes.
 *
 * The sheet and the hub share one enum; these tests are what make "every tile
 * lands on a working screen" a checked property rather than a review note.
 */
class CreateSurfaceRoutingTest {

    private val json = Json

    @Test
    fun `every sheet tile maps to a route that opens the same surface`() {
        CreateSurface.entries.forEach { surface ->
            val route = CreateRoute.of(surface)
            assertThat(route.surface).isEqualTo(surface.routeKey)
            assertThat(CreateSurface.fromRouteKey(route.surface)).isEqualTo(surface)
        }
    }

    @Test
    fun `the sheet offers exactly the six typed tiles in the render's order`() {
        assertThat(CreateSurface.entries.map { it.label })
            .containsExactly("Text", "Photo", "Reel", "Audio", "Poll", "Article")
            .inOrder()
        assertThat(CreateSurface.entries.map { it.routeKey })
            .containsExactly("text", "photo", "reel", "audio", "poll", "article")
            .inOrder()
        // Live is a row that navigates elsewhere, never a composer surface.
        assertThat(CreateSurface.entries.map { it.routeKey }).doesNotContain("live")
    }

    @Test
    fun `route keys are stable lower-case tokens, not enum names`() {
        CreateSurface.entries.forEach { surface ->
            assertThat(surface.routeKey).isEqualTo(surface.routeKey.lowercase())
            assertThat(surface.routeKey).doesNotContain(" ")
        }
    }

    @Test
    fun `an unknown or missing key opens the text composer`() {
        assertThat(CreateSurface.fromRouteKey("live")).isEqualTo(CreateSurface.Text)
        assertThat(CreateSurface.fromRouteKey("")).isEqualTo(CreateSurface.Text)
        assertThat(CreateSurface.fromRouteKey(null)).isEqualTo(CreateSurface.Text)
        assertThat(CreateRoute().surface).isEqualTo("text")
    }

    /** The argument survives the trip through the saved back stack byte for byte. */
    @Test
    fun `CreateRoute serialises its surface and round-trips`() {
        val route = CreateRoute.of(CreateSurface.Audio)
        val encoded = json.encodeToString(CreateRoute.serializer(), route)
        assertThat(encoded).isEqualTo("""{"surface":"audio"}""")
        assertThat(json.decodeFromString(CreateRoute.serializer(), encoded)).isEqualTo(route)

        CreateSurface.entries.forEach { surface ->
            val wire = json.encodeToString(CreateRoute.serializer(), CreateRoute.of(surface))
            val back = json.decodeFromString(CreateRoute.serializer(), wire)
            assertThat(CreateSurface.fromRouteKey(back.surface)).isEqualTo(surface)
        }
    }

    @Test
    fun `elapsed time reads like a recorder`() {
        assertThat(formatElapsed(0)).isEqualTo("0:00")
        assertThat(formatElapsed(9_999)).isEqualTo("0:09")
        assertThat(formatElapsed(65_000)).isEqualTo("1:05")
        assertThat(formatElapsed(180_000)).isEqualTo("3:00")
    }
}
