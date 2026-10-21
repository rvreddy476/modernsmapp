package com.us.android.feature.auth.navigation

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The route arguments, as the web's query strings carry them. */
class AuthNavigationTest {

    @Test
    fun `step-up methods travel as one comma-joined string and come back as a list`() {
        val route = StepUpVerifyRoute.of("pt-2", listOf("email_otp", "totp"))

        assertThat(route.methods).isEqualTo("email_otp,totp")
        assertThat(route.methodList).containsExactly("email_otp", "totp").inOrder()
        assertThat(StepUpVerifyRoute("pt", "").methodList).isEmpty()
        assertThat(StepUpVerifyRoute("pt", "totp,").methodList).containsExactly("totp")
    }

    @Test
    fun `the password reset route defaults to the sign-in flow`() {
        val route = PasswordResetRoute()

        assertThat(route.fromSettings).isFalse()
        assertThat(route.identifier).isEmpty()
        assertThat(PasswordResetRoute("raghu@example.com", fromSettings = true).fromSettings).isTrue()
    }
}
