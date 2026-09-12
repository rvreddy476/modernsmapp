package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.network.CategoryDto
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * The category card decodes its live product count, and survives its absence.
 *
 * The server added `product_count` so the strip can dim a category that
 * opens onto nothing. The DTO used to drop it on the floor, which is why
 * MStore could not tell an empty category from a full one. It is defaulted
 * rather than required because the field is additive: a server that predates
 * it must still decode, and an empty count is the honest fallback.
 */
class CategoryDtoTest {

    private val json = NetworkModule.provideJson()

    @Test
    fun `a category card carries its product count and artwork`() {
        val dto = json.decodeFromString(
            CategoryDto.serializer(),
            """{"id":"c1","name":"Phones","slug":"phones","image_url":"https://obj/p.jpg","product_count":12}""",
        )

        assertThat(dto.productCount).isEqualTo(12)
        assertThat(dto.imageUrl).isEqualTo("https://obj/p.jpg")
        assertThat(dto.name).isEqualTo("Phones")
    }

    @Test
    fun `a card without a count decodes as empty rather than failing`() {
        val dto = json.decodeFromString(
            CategoryDto.serializer(),
            """{"id":"c1","name":"Phones","slug":"phones"}""",
        )

        assertThat(dto.productCount).isEqualTo(0)
        assertThat(dto.imageUrl).isNull()
    }
}
