package com.us.android.feature.kitchen.onboarding

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.food.model.OnboardingStep
import org.junit.Test

class KitchenChecklistTest {

    /** food-service onboarding.MissingSteps, in its presentation order. */
    private val vocabulary = listOf(
        "location", "state", "operating_hours", "compliance", "fssai_document", "payout_account", "menu_item",
    )

    private val expectedEditor = mapOf(
        "location" to StepEditor.LOCATION,
        "state" to StepEditor.LOCATION,
        "operating_hours" to StepEditor.OPERATING_HOURS,
        "compliance" to StepEditor.COMPLIANCE,
        "fssai_document" to StepEditor.FSSAI,
        "payout_account" to StepEditor.PAYOUT,
        "menu_item" to StepEditor.MENU,
    )

    @Test
    fun `rows follow the server's vocabulary and order`() {
        assertThat(KitchenChecklist.fromMissing(emptyList()).rows.map { it.step.wire })
            .containsExactlyElementsIn(vocabulary).inOrder()
    }

    @Test
    fun `each missing token marks exactly its own row to do and opens its editor`() {
        for (token in vocabulary) {
            val checklist = KitchenChecklist.fromMissing(listOf(token))

            val todo = checklist.rows.filter { it.status == RowStatus.TO_DO }
            assertWithMessage(token).that(todo.map { it.step.wire }).containsExactly(token)
            assertWithMessage(token).that(todo.single().editor).isEqualTo(expectedEditor.getValue(token))
            assertWithMessage(token).that(todo.single().title).isNotEmpty()
            assertThat(checklist.rows.count { it.status == RowStatus.DONE }).isEqualTo(vocabulary.size - 1)
            assertWithMessage(token).that(checklist.isReady).isFalse()
            assertThat(checklist.remaining).isEqualTo(1)
            assertThat(checklist.unrecognised).isEmpty()
        }
    }

    @Test
    fun `the state step is completed on the location screen`() {
        assertThat(KitchenChecklist.editorFor(OnboardingStep.STATE)).isEqualTo(StepEditor.LOCATION)
    }

    @Test
    fun `an empty missing list is ready`() {
        val checklist = KitchenChecklist.fromMissing(emptyList())
        assertThat(checklist.isReady).isTrue()
        assertThat(checklist.remaining).isEqualTo(0)
    }

    @Test
    fun `a step this build does not know keeps the checklist not ready`() {
        val checklist = KitchenChecklist.fromMissing(listOf("pan_verification"))
        assertThat(checklist.rows.all { it.status == RowStatus.DONE }).isTrue()
        assertThat(checklist.unrecognised).containsExactly("pan_verification")
        assertThat(checklist.isReady).isFalse()
        assertThat(checklist.remaining).isEqualTo(1)
    }

    @Test
    fun `before any server answer nothing is ticked and nothing is ready`() {
        val checklist = KitchenChecklist.unchecked()
        assertThat(checklist.rows.map { it.status }.toSet()).containsExactly(RowStatus.UNCHECKED)
        assertThat(checklist.isChecked).isFalse()
        assertThat(checklist.isReady).isFalse()
    }
}
