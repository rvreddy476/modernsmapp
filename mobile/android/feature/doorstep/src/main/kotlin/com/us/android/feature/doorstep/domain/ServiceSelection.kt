package com.us.android.feature.doorstep.domain

import com.us.android.feature.doorstep.data.AddonGroupDto
import com.us.android.feature.doorstep.data.ServiceDetailDto
import com.us.android.feature.doorstep.data.ServiceOptionDto
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.sum

/**
 * What the customer has picked on a service page: one option, a quantity,
 * and add-ons. Pure, so the rules are plain unit tests.
 *
 * The server re-validates everything on `POST /quotes` (DOORSTEP_OPTION_INVALID,
 * DOORSTEP_QUANTITY_INVALID, DOORSTEP_ADDON_INVALID) and prices it from its own
 * rows; these rules exist so the page never offers a Continue the server will
 * refuse, and so the preview total matches the quote that follows.
 */
data class ServiceSelection(
    val optionId: String?,
    val quantity: Int = 1,
    val addonIds: Set<String> = emptySet(),
    /** The customer's "require a woman professional" switch — sent only where [GenderRules] allows it. */
    val requireWoman: Boolean = false,
)

/** A broken add-on group rule, as the page shows it under the group. */
data class GroupViolation(val groupId: String, val message: String)

object SelectionRules {

    /** The default option (is_default, else the first), quantity 1, no add-ons. */
    fun initial(service: ServiceDetailDto): ServiceSelection =
        ServiceSelection(optionId = (service.options.firstOrNull { it.isDefault } ?: service.options.firstOrNull())?.id)

    fun option(service: ServiceDetailDto, selection: ServiceSelection): ServiceOptionDto? =
        service.options.firstOrNull { it.id == selection.optionId }

    /** Picks [optionId] and clamps the quantity into its range. */
    fun selectOption(service: ServiceDetailDto, selection: ServiceSelection, optionId: String): ServiceSelection {
        val option = service.options.firstOrNull { it.id == optionId } ?: return selection
        return selection.copy(optionId = option.id, quantity = selection.quantity.coerceIn(1, option.maxQuantity.coerceAtLeast(1)))
    }

    /** 1..option.max_quantity — the server's DOORSTEP_QUANTITY_INVALID range. */
    fun setQuantity(service: ServiceDetailDto, selection: ServiceSelection, quantity: Int): ServiceSelection {
        val max = option(service, selection)?.maxQuantity?.coerceAtLeast(1) ?: 1
        return selection.copy(quantity = quantity.coerceIn(1, max))
    }

    /**
     * Toggles an add-on. A pick-one group (max_select 1) swaps the choice; a
     * pick-many group refuses a pick past max_select rather than dropping
     * another one silently.
     */
    fun toggleAddon(service: ServiceDetailDto, selection: ServiceSelection, addonId: String): ServiceSelection {
        val group = service.addonGroups.firstOrNull { g -> g.addons.any { it.id == addonId } } ?: return selection
        val inGroup = group.addons.map { it.id }.toSet()
        val chosen = selection.addonIds intersect inGroup
        return when {
            addonId in chosen -> selection.copy(addonIds = selection.addonIds - addonId)
            group.maxSelect == 1 -> selection.copy(addonIds = selection.addonIds - inGroup + addonId)
            chosen.size >= group.maxSelect -> selection
            else -> selection.copy(addonIds = selection.addonIds + addonId)
        }
    }

    /** The fewest picks a group accepts: max(min_select, is_required ? 1 : 0) — the server's rule. */
    fun required(group: AddonGroupDto): Int = maxOf(group.minSelect, if (group.isRequired) 1 else 0)

    /** Every group whose min/max is broken by [selection]. */
    fun violations(service: ServiceDetailDto, selection: ServiceSelection): List<GroupViolation> =
        service.addonGroups.mapNotNull { group ->
            val count = group.addons.count { it.id in selection.addonIds }
            val need = required(group)
            when {
                count < need -> GroupViolation(group.id, if (need == 1) "Choose one" else "Choose at least $need")
                count > group.maxSelect -> GroupViolation(group.id, "Choose up to ${group.maxSelect}")
                else -> null
            }
        }

    /** Whether the page may ask the server for a quote. */
    fun isComplete(service: ServiceDetailDto, selection: ServiceSelection): Boolean {
        val option = option(service, selection) ?: return false
        return selection.quantity in 1..option.maxQuantity && violations(service, selection).isEmpty()
    }

    /**
     * The preview total, GST-inclusive like every catalogue price: the option
     * per unit × quantity plus each chosen add-on once. The quote that follows
     * states the real figure; this only has to agree with it.
     */
    fun estimate(service: ServiceDetailDto, selection: ServiceSelection): Paise {
        val option = option(service, selection) ?: return Paise.ZERO
        val addons = chosenAddons(service, selection).map { Paise(it.pricePaise) }.sum()
        return Paise(option.pricePaise) * selection.quantity + addons
    }

    /** Minutes: the option's unit duration × quantity plus each add-on's extra minutes. */
    fun durationMinutes(service: ServiceDetailDto, selection: ServiceSelection): Int {
        val option = option(service, selection) ?: return service.durationMinutes
        return option.durationMinutes * selection.quantity + chosenAddons(service, selection).sumOf { it.extraDurationMinutes }
    }

    private fun chosenAddons(service: ServiceDetailDto, selection: ServiceSelection) =
        service.addonGroups.flatMap { it.addons }.filter { it.id in selection.addonIds }
}

/**
 * The gender rules a customer sees (founder, 4 Oct): women's salon is done by
 * women only, men's salon by men only, and in every OTHER category the
 * customer may require a woman professional.
 */
object GenderRules {
    const val ANY = "any"
    const val FEMALE_ONLY = "female_pros_only"
    const val MALE_ONLY = "male_pros_only"

    /** The "require a woman professional" switch is offered only where the category leaves the choice open. */
    fun womanPreferenceOffered(genderRule: String): Boolean = genderRule == ANY

    /**
     * The `require_female_pro` value sent to the server. A men's-salon booking
     * can never carry it (no professional could qualify), and a women's-salon
     * booking needs no preference: the category rule already decides.
     */
    fun requireFemalePro(genderRule: String, switchOn: Boolean): Boolean = womanPreferenceOffered(genderRule) && switchOn

    /** The note a category with a fixed rule shows instead of the switch; null for "any". */
    fun fixedRuleNote(genderRule: String): String? = when (genderRule) {
        FEMALE_ONLY -> "Done by women professionals only"
        MALE_ONLY -> "Done by men professionals only"
        else -> null
    }
}
