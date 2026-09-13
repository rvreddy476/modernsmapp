package com.us.android.feature.kitchen.money

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.MenuItemDto
import com.us.android.core.food.network.MenuItemRequest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.math.BigDecimal

class RupeeFormatTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `paise format as rupees with Indian grouping and two decimals`() {
        assertThat(RupeeFormat.format(Paise(0))).isEqualTo("₹0.00")
        assertThat(RupeeFormat.format(Paise(5))).isEqualTo("₹0.05")
        assertThat(RupeeFormat.format(Paise(100))).isEqualTo("₹1.00")
        assertThat(RupeeFormat.format(Paise(24_950))).isEqualTo("₹249.50")
        assertThat(RupeeFormat.format(Paise(99_999))).isEqualTo("₹999.99")
        assertThat(RupeeFormat.format(Paise(100_000))).isEqualTo("₹1,000.00")
        assertThat(RupeeFormat.format(Paise(12_345_678))).isEqualTo("₹1,23,456.78")
        assertThat(RupeeFormat.format(Paise(100_000_000))).isEqualTo("₹10,00,000.00")
        assertThat(RupeeFormat.format(Paise(-150))).isEqualTo("-₹1.50")
        // 92,233,720,368,547,758.08 rupees, grouped the Indian way.
        assertThat(RupeeFormat.format(Paise(Long.MIN_VALUE))).isEqualTo("-₹92,23,37,20,36,85,47,758.08")
    }

    @Test
    fun `typed prices parse exactly into paise`() {
        assertThat(RupeeFormat.parse("249")).isEqualTo(Paise(24_900))
        assertThat(RupeeFormat.parse("249.5")).isEqualTo(Paise(24_950))
        assertThat(RupeeFormat.parse("249.50")).isEqualTo(Paise(24_950))
        assertThat(RupeeFormat.parse(" ₹1,249.05 ")).isEqualTo(Paise(124_905))
        assertThat(RupeeFormat.parse("0.10")).isEqualTo(Paise(10))
        // 0.1 + 0.2 as a Double is 0.30000000000000004 — never produced here.
        assertThat(RupeeFormat.parse("0.3")).isEqualTo(Paise(30))
    }

    @Test
    fun `malformed prices are refused rather than rounded`() {
        for (input in listOf("", "abc", "-5", "249.999", "1e3", "12.", ".5", "1234567890", "₹")) {
            assertWithMessage(input).that(RupeeFormat.parse(input)).isNull()
        }
    }

    @Test
    fun `entry text round-trips`() {
        assertThat(RupeeFormat.toEntryText(Paise(24_950))).isEqualTo("249.50")
        assertThat(RupeeFormat.parse(RupeeFormat.toEntryText(Paise(7)))).isEqualTo(Paise(7))
    }

    @Test
    fun `float-rupee wire values decode to paise through decimal text`() {
        fun price(raw: String) = json.decodeFromString(MenuItemDto.serializer(), """{"id":"i","base_price":$raw}""").basePrice
        assertThat(price("249.5")).isEqualTo(Paise(24_950))
        assertThat(price("100")).isEqualTo(Paise(10_000))
        assertThat(price("0.30000000000000004")).isEqualTo(Paise(30))
        assertThat(price("1e3")).isEqualTo(Paise(100_000))
        assertThat(price("19.995")).isEqualTo(Paise(2_000))
    }

    @Test
    fun `paise encode as an exact unquoted rupee decimal`() {
        val body = json.encodeToString(
            MenuItemRequest.serializer(),
            MenuItemRequest(
                name = "Dosa",
                foodType = "VEG",
                basePrice = Paise(24_950),
                discountPrice = Paise(19_900),
                preparationMinutes = 15,
                isRecommended = false,
                taxPercentage = BigDecimal("5"),
            ),
        )
        assertThat(body).contains("\"base_price\":249.50")
        assertThat(body).contains("\"discount_price\":199.00")
        assertThat(body).contains("\"tax_percentage\":5")
        assertThat(body).contains("\"is_recommended\":false")
        assertThat(body).contains("\"preparation_minutes\":15")
    }
}
