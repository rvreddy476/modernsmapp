package com.us.android.feature.kitchen.onboarding

import com.us.android.core.food.model.OnboardingChecklist
import com.us.android.core.food.model.OnboardingStep

/** The screen that completes a step. `state` has no screen of its own: it is set on Location. */
enum class StepEditor { LOCATION, OPERATING_HOURS, COMPLIANCE, FSSAI, PAYOUT, MENU }

enum class RowStatus {
    /** The server's last `missing[]` did not list this step. */
    DONE,

    /** The server's last `missing[]` listed it. */
    TO_DO,

    /** No server answer yet (see [KitchenChecklist.unchecked]). */
    UNCHECKED,
}

data class ChecklistRow(
    val step: OnboardingStep,
    val title: String,
    val detail: String,
    val editor: StepEditor,
    val status: RowStatus,
)

/**
 * The onboarding checklist as the partner sees it, built ONLY from the
 * server's `missing[]` ([OnboardingChecklist]). Saving a step never ticks it:
 * the server is the only judge of readiness.
 *
 * ROUTE GAP: food-service has no read of `missing[]`. It arrives only in the
 * 422 FOOD_RESTAURANT_NOT_READY answer to `POST …/submit`, so until the partner
 * submits once the rows are [RowStatus.UNCHECKED]. Reported with Feast A3 as a
 * missing `GET /v1/food/partner/restaurants/:id/onboarding`.
 */
data class KitchenChecklist(
    val rows: List<ChecklistRow>,
    /** Steps the server named that this build has no row for. Keeps the list not-ready. */
    val unrecognised: List<String>,
) {
    val isChecked: Boolean get() = rows.none { it.status == RowStatus.UNCHECKED }

    val isReady: Boolean get() = isChecked && unrecognised.isEmpty() && rows.all { it.status == RowStatus.DONE }

    val remaining: Int get() = rows.count { it.status != RowStatus.DONE } + unrecognised.size

    companion object {
        fun fromMissing(missing: List<String>): KitchenChecklist {
            val checklist = OnboardingChecklist.fromMissing(missing)
            return KitchenChecklist(
                rows = checklist.items.map { row(it.step, if (it.complete) RowStatus.DONE else RowStatus.TO_DO) },
                unrecognised = checklist.unrecognised,
            )
        }

        fun unchecked(): KitchenChecklist = KitchenChecklist(
            rows = OnboardingStep.entries.map { row(it, RowStatus.UNCHECKED) },
            unrecognised = emptyList(),
        )

        fun editorFor(step: OnboardingStep): StepEditor = when (step) {
            OnboardingStep.LOCATION, OnboardingStep.STATE -> StepEditor.LOCATION
            OnboardingStep.OPERATING_HOURS -> StepEditor.OPERATING_HOURS
            OnboardingStep.COMPLIANCE -> StepEditor.COMPLIANCE
            OnboardingStep.FSSAI_DOCUMENT -> StepEditor.FSSAI
            OnboardingStep.PAYOUT_ACCOUNT -> StepEditor.PAYOUT
            OnboardingStep.MENU_ITEM -> StepEditor.MENU
        }

        private fun row(step: OnboardingStep, status: RowStatus): ChecklistRow {
            val (title, detail) = copyFor(step)
            return ChecklistRow(step, title, detail, editorFor(step), status)
        }

        private fun copyFor(step: OnboardingStep): Pair<String, String> = when (step) {
            OnboardingStep.LOCATION -> "Restaurant location" to "Pin, address and how far you deliver"
            OnboardingStep.STATE -> "State" to "The state your kitchen is registered in, for GST"
            OnboardingStep.OPERATING_HOURS -> "Opening hours" to "When you take orders, in India time"
            OnboardingStep.COMPLIANCE -> "Tax details" to "Tax category, PAN and GSTIN"
            OnboardingStep.FSSAI_DOCUMENT -> "FSSAI licence" to "Licence number, expiry and a photo"
            OnboardingStep.PAYOUT_ACCOUNT -> "Bank account" to "Where your earnings are paid"
            OnboardingStep.MENU_ITEM -> "Menu" to "At least one dish customers can order"
        }
    }
}
