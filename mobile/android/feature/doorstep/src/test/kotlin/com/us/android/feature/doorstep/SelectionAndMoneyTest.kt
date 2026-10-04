package com.us.android.feature.doorstep

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorstep.domain.GenderRules
import com.us.android.feature.doorstep.domain.SelectionRules
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.sum
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.model.toShortRupeeText
import org.junit.Test

/**
 * The service page's rules against the golden women's-salon facial: the
 * preview total and duration agree with the server's quote for the same
 * pick, the add-on groups' min/max are kept, and the gender switch obeys the
 * category rule. Plus the paise arithmetic everything else stands on.
 */
class SelectionAndMoneyTest {

    private val facial = Fixtures.salonService().service
    private val gold = "3418c19d-9e25-5e7d-89a9-57d66209a97f"
    private val fruit = "8efcba0c-3558-5330-aca3-b4f69e3293a5"
    private val peel = "8bed2ebf-890e-59a2-afbd-910d5aa60cc6"
    private val charcoal = "2d492586-bb1a-5131-94ff-5a08fdc929ea"
    private val threading = "7fb9a6fd-59e9-598c-be77-89718543a6bf"
    private val massage = "da1d8a3b-2a87-535c-8619-3003cd0f65df"

    // ── Paise ──

    @Test
    fun `paise add, multiply and format exactly, with Indian grouping`() {
        assertThat(Paise(179900) + Paise(44900)).isEqualTo(Paise(224800))
        assertThat(Paise(9900) * 3).isEqualTo(Paise(29700))
        assertThat(listOf(Paise(129900), Paise(19900), Paise(29900)).sum()).isEqualTo(Paise(179700))
        assertThat(Paise(224800).toRupeeText()).isEqualTo("₹2,248.00")
        assertThat(Paise(1234567890).toRupeeText()).isEqualTo("₹1,23,45,678.90")
        assertThat(Paise(24900).toShortRupeeText()).isEqualTo("₹249")
        assertThat(Paise(24950).toShortRupeeText()).isEqualTo("₹249.50")
        assertThat(Paise(-7500).toRupeeText()).isEqualTo("-₹75.00")
    }

    @Test(expected = ArithmeticException::class)
    fun `paise never overflow silently`() {
        Paise(Long.MAX_VALUE) + Paise(1)
    }

    // ── Selection ──

    @Test
    fun `the preview matches the server's quote for the same pick`() {
        // quote_post_201_salon.json: Gold facial + Charcoal mask + Head massage = 179700 paise, 85 minutes.
        var s = SelectionRules.initial(facial)
        s = SelectionRules.toggleAddon(facial, s, charcoal)
        s = SelectionRules.toggleAddon(facial, s, massage)
        val quote = Fixtures.quote("quote_post_201_salon.json")
        assertThat(SelectionRules.estimate(facial, s)).isEqualTo(Paise(quote.totalPaise))
        assertThat(SelectionRules.durationMinutes(facial, s)).isEqualTo(quote.durationMinutes)
        assertThat(SelectionRules.isComplete(facial, s)).isTrue()
    }

    @Test
    fun `a required pick-one group must have exactly one`() {
        val initial = SelectionRules.initial(facial)
        assertThat(SelectionRules.violations(facial, initial).map { it.message }).containsExactly("Choose one")
        val masked = SelectionRules.toggleAddon(facial, initial, peel)
        assertThat(SelectionRules.violations(facial, masked)).isEmpty()
        // Pick-one swaps rather than adding a second mask.
        val swapped = SelectionRules.toggleAddon(facial, masked, charcoal)
        assertThat(swapped.addonIds).containsExactly(charcoal)
        // Tapping the chosen one clears it, and the group is unfinished again.
        val cleared = SelectionRules.toggleAddon(facial, swapped, charcoal)
        assertThat(SelectionRules.isComplete(facial, cleared)).isFalse()
    }

    @Test
    fun `a pick-many group refuses a pick past its max instead of dropping one`() {
        var s = SelectionRules.toggleAddon(facial, SelectionRules.initial(facial), peel)
        s = SelectionRules.toggleAddon(facial, s, threading)
        s = SelectionRules.toggleAddon(facial, s, massage)
        assertThat(s.addonIds).containsExactly(peel, threading, massage)
        // The optional group's max is 2: a third pick would break it, so it is refused.
        val group = facial.addonGroups.single { it.maxSelect == 2 }
        val third = group.addons.first().copy(id = "x-third")
        val widened = facial.copy(addonGroups = facial.addonGroups.map { if (it.id == group.id) it.copy(addons = it.addons + third) else it })
        assertThat(SelectionRules.toggleAddon(widened, s, "x-third")).isEqualTo(s)
        // A broken max (from a stale selection) is reported, not hidden.
        val over = s.copy(addonIds = s.addonIds + "x-third")
        assertThat(SelectionRules.violations(widened, over).map { it.message }).containsExactly("Choose up to 2")
    }

    @Test
    fun `quantity stays within one and the option's max, and the price follows it`() {
        val option = facial.options.first { it.id == gold }.copy(maxQuantity = 3)
        val service = facial.copy(options = facial.options.map { if (it.id == gold) option else it })
        var s = SelectionRules.toggleAddon(service, SelectionRules.initial(service), peel)
        s = SelectionRules.setQuantity(service, s, 5)
        assertThat(s.quantity).isEqualTo(3)
        assertThat(SelectionRules.estimate(service, s)).isEqualTo(Paise(129900) * 3 + Paise(9900))
        s = SelectionRules.setQuantity(service, s, 0)
        assertThat(s.quantity).isEqualTo(1)
        // Moving to an option with a smaller max clamps the quantity.
        s = SelectionRules.setQuantity(service, s, 3)
        s = SelectionRules.selectOption(service, s, fruit)
        assertThat(s.quantity).isEqualTo(1)
        assertThat(SelectionRules.estimate(service, s)).isEqualTo(Paise(69900) + Paise(9900))
    }

    // ── Gender rule ──

    @Test
    fun `the woman-professional switch is offered only where the category leaves it open`() {
        assertThat(GenderRules.womanPreferenceOffered(GenderRules.ANY)).isTrue()
        assertThat(GenderRules.womanPreferenceOffered(GenderRules.FEMALE_ONLY)).isFalse()
        assertThat(GenderRules.womanPreferenceOffered(GenderRules.MALE_ONLY)).isFalse()
    }

    @Test
    fun `a men's-salon booking never asks for a woman, and an open category sends the switch as set`() {
        assertThat(GenderRules.requireFemalePro(GenderRules.MALE_ONLY, switchOn = true)).isFalse()
        assertThat(GenderRules.requireFemalePro(GenderRules.FEMALE_ONLY, switchOn = true)).isFalse()
        assertThat(GenderRules.requireFemalePro(GenderRules.ANY, switchOn = true)).isTrue()
        assertThat(GenderRules.requireFemalePro(GenderRules.ANY, switchOn = false)).isFalse()
        assertThat(GenderRules.fixedRuleNote(GenderRules.FEMALE_ONLY)).isEqualTo("Done by women professionals only")
        assertThat(GenderRules.fixedRuleNote(GenderRules.ANY)).isNull()
    }
}
