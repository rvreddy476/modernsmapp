package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.OnboardingChecklist
import com.us.android.core.food.model.OnboardingStep
import org.junit.Test

class OnboardingChecklistTest {

    @Test
    fun `an empty missing list is ready and every step is complete`() {
        val checklist = OnboardingChecklist.fromMissing(emptyList())

        assertThat(checklist.isReady).isTrue()
        assertThat(checklist.nextStep).isNull()
        assertThat(checklist.items.all { it.complete }).isTrue()
    }

    @Test
    fun `items follow the server's presentation order whatever order missing arrives in`() {
        val checklist = OnboardingChecklist.fromMissing(listOf("menu_item", "location", "payout_account"))

        assertThat(checklist.items.map { it.step }).containsExactly(
            OnboardingStep.LOCATION,
            OnboardingStep.OPERATING_HOURS,
            OnboardingStep.COMPLIANCE,
            OnboardingStep.FSSAI_DOCUMENT,
            OnboardingStep.PAYOUT_ACCOUNT,
            OnboardingStep.MENU_ITEM,
        ).inOrder()
        assertThat(checklist.items.filterNot { it.complete }.map { it.step })
            .containsExactly(OnboardingStep.LOCATION, OnboardingStep.PAYOUT_ACCOUNT, OnboardingStep.MENU_ITEM)
            .inOrder()
        assertThat(checklist.nextStep).isEqualTo(OnboardingStep.LOCATION)
    }

    @Test
    fun `a step this build does not know keeps the checklist not ready`() {
        val checklist = OnboardingChecklist.fromMissing(listOf("pan_verification"))

        assertThat(checklist.items.all { it.complete }).isTrue()
        assertThat(checklist.unrecognised).containsExactly("pan_verification")
        assertThat(checklist.isReady).isFalse()
    }

    @Test
    fun `the wire codes are food-service's step names`() {
        assertThat(OnboardingStep.entries.map { it.wire }).containsExactly(
            "location", "operating_hours", "compliance", "fssai_document", "payout_account", "menu_item",
        ).inOrder()
    }
}
