package com.us.android.feature.rider.onboarding

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test

class RiderChecklistTest {

    @Test
    fun `vehicle classes match riderkyc's ClassifyVehicle vectors`() {
        val cases = mapOf(
            "BICYCLE" to VehicleClass.BICYCLE, "bicycle" to VehicleClass.BICYCLE, " Cycle " to VehicleClass.BICYCLE,
            "MOTORCYCLE" to VehicleClass.MOTORISED, "bike" to VehicleClass.MOTORISED, "SCOOTER" to VehicleClass.MOTORISED,
            "ev_scooter" to VehicleClass.MOTORISED,
            "" to VehicleClass.UNKNOWN, "truck" to VehicleClass.UNKNOWN,
        )
        for ((input, want) in cases) {
            assertWithMessage(input).that(Vehicle.classify(input)).isEqualTo(want)
        }
        assertThat(Vehicle.classify("pedal-cycle")).isEqualTo(VehicleClass.BICYCLE)
        assertThat(Vehicle.classify("electric bike")).isEqualTo(VehicleClass.MOTORISED)
        assertThat(Vehicle.CHOICES.map { Vehicle.classify(it.wire) }).doesNotContain(VehicleClass.UNKNOWN)
    }

    @Test
    fun `a bicycle skips the driving licence and RC, and nothing else`() {
        assertThat(Vehicle.drivingDocumentsRequired("BICYCLE")).isFalse()
        assertThat(Vehicle.drivingDocumentsRequired("MOTORCYCLE")).isTrue()
        // Fails closed: an unknown vehicle still owes them.
        assertThat(Vehicle.drivingDocumentsRequired("truck")).isTrue()
        assertThat(Vehicle.drivingDocumentsRequired("")).isTrue()

        // riderkyc "bicycle with nothing": aadhaar_digilocker, selfie, payout_account.
        val bicycle = RiderChecklist.forVehicle(listOf("aadhaar_digilocker", "selfie", "payout_account"), "BICYCLE")
        assertThat(statusOf(bicycle, RiderStep.DRIVING_LICENCE)).isEqualTo(RowStatus.NOT_NEEDED)
        assertThat(statusOf(bicycle, RiderStep.VEHICLE_RC)).isEqualTo(RowStatus.NOT_NEEDED)
        assertThat(statusOf(bicycle, RiderStep.SELFIE)).isEqualTo(RowStatus.TO_DO)
        assertThat(bicycle.nextStep).isEqualTo(RiderStep.AADHAAR_DIGILOCKER)

        // The same verified facts on a motorcycle owe DL and RC.
        val motorcycle = RiderChecklist.forVehicle(listOf("driving_licence", "vehicle_rc"), "MOTORCYCLE")
        assertThat(statusOf(motorcycle, RiderStep.DRIVING_LICENCE)).isEqualTo(RowStatus.TO_DO)
        assertThat(motorcycle.isReady).isFalse()

        // "bicycle complete without DL or RC" is ready.
        assertThat(RiderChecklist.forVehicle(emptyList(), "bicycle").isReady).isTrue()
    }

    @Test
    fun `the full vocabulary is presented in the server's order`() {
        val all = RiderChecklist.from(
            listOf("vehicle", "aadhaar_digilocker", "driving_licence", "vehicle_rc", "selfie", "payout_account"),
            drivingDocumentsRequired = true,
        )
        assertThat(all.rows.map { it.step.wire })
            .containsExactly("vehicle", "aadhaar_digilocker", "driving_licence", "vehicle_rc", "selfie", "payout_account").inOrder()
        assertThat(all.rows.map { it.status }.toSet()).containsExactly(RowStatus.TO_DO)
        assertThat(all.nextStep).isEqualTo(RiderStep.VEHICLE)
    }

    @Test
    fun `the server wins, and an unknown step fails closed`() {
        // A bicycle profile the server still asks DL for: shown as to-do.
        val serverSays = RiderChecklist.from(listOf("driving_licence"), drivingDocumentsRequired = false)
        assertThat(statusOf(serverSays, RiderStep.DRIVING_LICENCE)).isEqualTo(RowStatus.TO_DO)

        val unknown = RiderChecklist.from(listOf("police_verification"), drivingDocumentsRequired = true)
        assertThat(unknown.isReady).isFalse()
        assertThat(unknown.unrecognised).containsExactly("police_verification")
    }

    private fun statusOf(checklist: RiderChecklist, step: RiderStep): RowStatus =
        checklist.rows.single { it.step == step }.status
}
