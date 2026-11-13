package com.us.android.di

import com.google.common.truth.Truth.assertThat
import com.us.android.BuildConfig
import org.junit.Test

/**
 * The founder's rule (13 Nov 2026): Premium is never sold from a release
 * build until Google Play's payments policy has been checked. The flag is on
 * in debug builds only, and the switch is exactly the flag.
 */
class PremiumSwitchModuleTest {

    @Test
    fun `the purchase switch is on in debug builds only`() {
        assertThat(BuildConfig.PREMIUM_PURCHASE_ENABLED).isEqualTo(BuildConfig.BUILD_TYPE == "debug")
        if (BuildConfig.BUILD_TYPE == "release") assertThat(BuildConfig.PREMIUM_PURCHASE_ENABLED).isFalse()
    }

    @Test
    fun `the switch is exactly the build flag`() {
        assertThat(PremiumSwitchModule.premiumPurchaseSwitch(false).enabled).isFalse()
        assertThat(PremiumSwitchModule.premiumPurchaseSwitch(true).enabled).isTrue()
        val provided = PremiumSwitchModule.providePremiumPurchaseSwitch()
        assertThat(provided.enabled).isEqualTo(BuildConfig.PREMIUM_PURCHASE_ENABLED)
    }
}
