package com.us.android.feature.rider.profile

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class ProfileRulesTest {

    private val motorcycle = ProfileForm(fullName = "Test Rider", phone = "+91 98765 43210", vehicleType = "MOTORCYCLE", vehicleNumber = "ka-01-ab-1234")

    @Test
    fun `a motorised vehicle needs a well-formed registration, sent normalised`() {
        assertThat(ProfileRules.validate(motorcycle)).isEmpty()
        assertThat(ProfileRules.request(motorcycle).vehicleNumber).isEqualTo("KA01AB1234")
        assertThat(ProfileRules.request(motorcycle).phone).isEqualTo("9876543210")

        val bad = motorcycle.copy(vehicleNumber = "ZZ12AB1234")
        assertThat(ProfileRules.validate(bad).keys).containsExactly(ProfileField.VEHICLE_NUMBER)
    }

    @Test
    fun `a bicycle needs no registration and sends none`() {
        val bicycle = motorcycle.copy(vehicleType = "BICYCLE", vehicleNumber = "not a plate")
        assertThat(ProfileRules.validate(bicycle)).isEmpty()
        assertThat(ProfileRules.request(bicycle).vehicleNumber).isEmpty()
        assertThat(ProfileUiState(form = bicycle).needsRegistration).isFalse()
        assertThat(ProfileUiState(form = motorcycle).needsRegistration).isTrue()
    }

    @Test
    fun `name, phone and a vehicle choice are required`() {
        val empty = ProfileRules.validate(ProfileForm())
        assertThat(empty.keys).containsExactly(ProfileField.NAME, ProfileField.PHONE, ProfileField.VEHICLE_TYPE)
        assertThat(ProfileRules.normalizedPhone("98765 4321")).isNull()
        assertThat(ProfileRules.normalizedPhone("919876543210")).isEqualTo("9876543210")
    }
}
