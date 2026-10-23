package com.us.android.core.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.memberships.data.LevelRules
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.rupees
import org.junit.Test

/**
 * The level rules match the server's exactly (service/membership.go:
 * `MinTierPricePaise` 2,900, `MaxTierPricePaise` 2,99,900, `MaxTierNameLength`
 * 60 runes, `MaxTierPerks` 10, `MaxTierPerkLength` 100 runes, `MaxActiveTiers`
 * 3), at every boundary, so the form never sends what the server refuses and
 * never refuses what it would take.
 */
class LevelRulesTest {

    @Test
    fun `a name is 1 to 60 characters after trimming, counted as the server counts runes`() {
        assertThat(LevelRules.nameProblem("")).isNotNull()
        assertThat(LevelRules.nameProblem("   ")).isNotNull()
        assertThat(LevelRules.nameProblem("A")).isNull()
        assertThat(LevelRules.nameProblem("a".repeat(60))).isNull()
        assertThat(LevelRules.nameProblem("a".repeat(61))).isNotNull()
        assertThat(LevelRules.nameProblem("  ${"a".repeat(60)}  ")).isNull()
        // 60 emoji are 120 UTF-16 units but 60 runes: allowed, as the server allows them.
        assertThat(LevelRules.nameProblem("🔥".repeat(60))).isNull()
        assertThat(LevelRules.nameProblem("🔥".repeat(61))).isNotNull()
    }

    @Test
    fun `a price is whole rupees from 29 to 2,999`() {
        assertThat(LevelRules.parseRupees("99")).isEqualTo(9_900)
        assertThat(LevelRules.parseRupees(" ₹1,499 ")).isEqualTo(149_900)
        assertThat(LevelRules.parseRupees("99.50")).isNull()
        assertThat(LevelRules.parseRupees("-29")).isNull()
        assertThat(LevelRules.parseRupees("")).isNull()
        assertThat(LevelRules.parseRupees("9".repeat(30))).isNull()

        assertThat(LevelRules.priceProblem(2_899)).isNotNull()
        assertThat(LevelRules.priceProblem(2_900)).isNull()
        assertThat(LevelRules.priceProblem(299_900)).isNull()
        assertThat(LevelRules.priceProblem(299_901)).isNotNull()
        assertThat(LevelRules.priceProblem(null)).isNotNull()
        assertThat(LevelRules.priceProblem(LevelRules.parseRupees("28"))).isNotNull()
        assertThat(LevelRules.priceProblem(LevelRules.parseRupees("2999"))).isNull()
        assertThat(LevelRules.priceProblem(LevelRules.parseRupees("3000"))).isNotNull()
    }

    @Test
    fun `up to ten perks of up to a hundred characters, blanks dropped first`() {
        assertThat(LevelRules.perksProblem(List(10) { "perk $it" })).isNull()
        assertThat(LevelRules.perksProblem(List(11) { "perk $it" })).isNotNull()
        // Blank lines are not perks: ten real ones and three blanks is ten.
        assertThat(LevelRules.perksProblem(List(10) { "perk $it" } + listOf("", "  ", "\t"))).isNull()
        assertThat(LevelRules.perksProblem(listOf("p".repeat(100)))).isNull()
        assertThat(LevelRules.perksProblem(listOf("p".repeat(101)))).isNotNull()
        assertThat(LevelRules.normalisePerks(listOf(" a ", "", "b"))).containsExactly("a", "b").inOrder()
    }

    @Test
    fun `at most three active levels`() {
        val two = List(2) { level("t$it", active = true) } + level("off", active = false)
        val three = List(3) { level("t$it", active = true) }
        assertThat(LevelRules.canAddActive(two)).isTrue()
        assertThat(LevelRules.canAddActive(three)).isFalse()
        assertThat(LevelRules.canAddActive(three + level("off", active = false))).isFalse()
    }

    @Test
    fun `every problem is said, in the form's order`() {
        assertThat(LevelRules.problems("", "10", List(11) { "x" })).hasSize(3)
        assertThat(LevelRules.problems("Supporter", "99", listOf("Early access"))).isEmpty()
    }

    @Test
    fun `money reads in rupees with the Indian grouping and never a float`() {
        assertThat(rupees(2_900)).isEqualTo("₹29")
        assertThat(rupees(299_900)).isEqualTo("₹2,999")
        assertThat(rupees(26_666)).isEqualTo("₹266.66")
        assertThat(rupees(10_000_000)).isEqualTo("₹1,00,000")
        assertThat(rupees(5)).isEqualTo("₹0.05")
    }

    private fun level(id: String, active: Boolean) = MembershipLevel(
        id = id,
        creatorId = "c",
        name = id,
        pricePaise = 9_900,
        perks = emptyList(),
        memberCount = 0,
        isActive = active,
    )
}
