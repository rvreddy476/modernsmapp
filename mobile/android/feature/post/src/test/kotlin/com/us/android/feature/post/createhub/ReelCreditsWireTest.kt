package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.credits.CreditInputDto
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.network.di.NetworkModule
import com.us.android.feature.post.data.dto.CreatePostRequest
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Test
import java.io.File

/**
 * The create request's credits (contract pinned 2026-10-23), as bytes: a
 * long video carries `credits`, `genres`, `release_year` and
 * `production_house` when set; a reel never does (the server answers 400
 * CREDITS_NOT_ALLOWED), and a long video without credits is the request it
 * was before they existed.
 */
class ReelCreditsWireTest {

    private val json = NetworkModule.provideJson()

    private fun CreatePostRequest.wire(): JsonObject =
        json.parseToJsonElement(json.encodeToString(CreatePostRequest.serializer(), this)).jsonObject

    private val withCredits = PendingReelPublish(
        creationKey = "k",
        videoUri = "content://v",
        kind = PublishKind.LONG,
        title = "Monsoon",
        category = "film-animation",
        credits = listOf(CreditInputDto("Asha Rao", "Director", "u1"), CreditInputDto("Crew", "Editor")),
        genres = listOf("drama", "thriller"),
        releaseYear = 2024,
        productionHouse = " Blue Hour Films ",
    )

    @Test
    fun `a long video sends its credits in order, linked ones with user_id`() {
        val wire = ReelPublishPipeline.buildRequest(withCredits, "v", "c", language = "en").wire()

        val credits = wire.getValue("credits").jsonArray.map { it.jsonObject }
        assertThat(credits.map { it.getValue("name").jsonPrimitive.content }).containsExactly("Asha Rao", "Crew").inOrder()
        assertThat(credits.map { it.getValue("role").jsonPrimitive.content }).containsExactly("Director", "Editor").inOrder()
        assertThat(credits[0].getValue("user_id").jsonPrimitive.content).isEqualTo("u1")
        assertThat(credits[1].keys).doesNotContain("user_id")
        assertThat(wire.getValue("genres").jsonArray.map { it.jsonPrimitive.content })
            .containsExactly("drama", "thriller").inOrder()
        assertThat(wire.getValue("release_year").jsonPrimitive.content).isEqualTo("2024")
        assertThat(wire.getValue("production_house").jsonPrimitive.content).isEqualTo("Blue Hour Films")
    }

    @Test
    fun `a reel never sends credits, whatever the record holds`() {
        val wire = ReelPublishPipeline.buildRequest(withCredits.copy(kind = PublishKind.REEL), "v", "c", language = "en").wire()

        assertThat(wire.keys).containsNoneOf("credits", "genres", "release_year", "production_house")
    }

    @Test
    fun `a long video without credits is byte-identical to one from before credits`() {
        val bare = withCredits.copy(credits = emptyList(), genres = emptyList(), releaseYear = null, productionHouse = "  ")
        val wire = ReelPublishPipeline.buildRequest(bare, "v", "c", language = "en").wire()

        assertThat(wire.keys).containsNoneOf("credits", "genres", "release_year", "production_house")
    }

    /**
     * post-service's `create_request.json` (copied byte for byte into
     * `src/test/resources/contracts/credits`). The fixture is the server's
     * request struct marshalled whole, so its unset fields are Go zero values
     * (`null`, `""`, `0`, `false`), while the app's create carries its own
     * upload choices (`distribution`, `allow_download`, …). So: the credits
     * block is the SAME JSON tree, and every field the fixture actually sets
     * is sent with the same value.
     */
    @Test
    fun `a long video's create matches the server's create_request fixture`() {
        val fixture = json.parseToJsonElement(File("src/test/resources/contracts/credits/create_request.json").readText()).jsonObject
        val pending = PendingReelPublish(
            creationKey = "k",
            videoUri = "content://v",
            kind = PublishKind.LONG,
            title = "Monsoon Letters",
            caption = "A short film about letters that never arrive.",
            category = "film_animation",
            credits = listOf(
                CreditInputDto("Asha Rao", "Director", "d3333333-3333-4333-8333-333333333333"),
                CreditInputDto("Ravi Kumar", "Actor", "d4444444-4444-4444-8444-444444444444"),
                CreditInputDto("Meera Iyer", "Music"),
            ),
            genres = listOf("drama", "romance"),
            releaseYear = 2024,
            productionHouse = "Blue Hour Films",
        )

        val wire = ReelPublishPipeline.buildRequest(pending, "d6666666-6666-4666-8666-666666666666", null, language = "en").wire()

        listOf("credits", "genres", "release_year", "production_house").forEach { key ->
            assertThat(wire[key]).isEqualTo(fixture.getValue(key))
        }
        val set = fixture.filterValues { !it.isGoZero() }
        assertThat(set.keys).containsAtLeast("text", "title", "content_type", "post_type", "media_ids", "credits")
        set.forEach { (key, value) -> assertThat(wire[key]).isEqualTo(value) }
    }

    private fun JsonElement.isGoZero(): Boolean = when (this) {
        is JsonNull -> true
        is JsonPrimitive -> content.isEmpty() || content == "0" || content == "false"
        else -> false
    }

    @Test
    fun `a record written before credits still loads`() {
        val old = """{"creationKey":"k","videoUri":"content://v","kind":"LONG","title":"T"}"""

        val loaded = json.decodeFromString(PendingReelPublish.serializer(), old)

        assertThat(loaded.credits).isEmpty()
        assertThat(loaded.releaseYear).isNull()
    }
}
