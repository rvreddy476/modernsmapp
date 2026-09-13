package com.us.android.core.food.model

/**
 * The restaurant onboarding steps, in the order food-service says a partner app
 * should present them (onboarding.MissingSteps).
 */
enum class OnboardingStep(val wire: String) {
    LOCATION("location"),

    /**
     * The restaurant's state resolves to a GST state (onboarding.StepState).
     * Without it checkout refuses every order with FOOD_RESTAURANT_STATE_UNKNOWN.
     * Added in A3 (2026-09-13): the server emits it, and an unknown code kept
     * every restaurant's checklist permanently not-ready.
     */
    STATE("state"),
    OPERATING_HOURS("operating_hours"),
    COMPLIANCE("compliance"),
    FSSAI_DOCUMENT("fssai_document"),
    PAYOUT_ACCOUNT("payout_account"),
    MENU_ITEM("menu_item"),
    ;

    companion object {
        fun fromWire(code: String): OnboardingStep? = entries.firstOrNull { it.wire == code }
    }
}

/**
 * A checklist built from the server's `missing[]`, which is the only source of
 * truth for readiness: the client never infers a step is done from its own
 * state.
 *
 * Fails closed: a code this build does not know (the server added a step)
 * keeps the checklist not-ready and is kept in [unrecognised] so the screen can
 * say "something else is needed" rather than showing a full checklist and a
 * submit that 422s.
 */
data class OnboardingChecklist(
    val items: List<Item>,
    val unrecognised: List<String>,
) {
    data class Item(val step: OnboardingStep, val complete: Boolean)

    val isReady: Boolean get() = unrecognised.isEmpty() && items.all { it.complete }

    /** The first incomplete step in presentation order, or null. */
    val nextStep: OnboardingStep? get() = items.firstOrNull { !it.complete }?.step

    companion object {
        fun fromMissing(missing: List<String>): OnboardingChecklist {
            val known = missing.mapNotNull(OnboardingStep::fromWire).toSet()
            return OnboardingChecklist(
                items = OnboardingStep.entries.map { Item(it, complete = it !in known) },
                unrecognised = missing.filter { OnboardingStep.fromWire(it) == null }.distinct(),
            )
        }
    }
}
