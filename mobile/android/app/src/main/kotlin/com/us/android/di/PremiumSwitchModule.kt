package com.us.android.di

import com.us.android.BuildConfig
import com.us.android.feature.premium.PremiumPurchaseSwitch
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * Momentum Premium's purchase switch (founder, 13 Nov 2026), from the build
 * flag `PREMIUM_PURCHASE_ENABLED` (app/build.gradle.kts): on in debug builds,
 * OFF in every release build until Google Play's payments policy for an
 * in-app digital pass has been checked. Off, `:feature:premium` draws no way
 * in and asks the server nothing.
 */
@Module
@InstallIn(SingletonComponent::class)
object PremiumSwitchModule {

    @Provides
    @Singleton
    fun providePremiumPurchaseSwitch(): PremiumPurchaseSwitch =
        premiumPurchaseSwitch(BuildConfig.PREMIUM_PURCHASE_ENABLED)

    /** The switch for a build flag. A function so the release rule is a test. */
    fun premiumPurchaseSwitch(buildFlag: Boolean): PremiumPurchaseSwitch = PremiumPurchaseSwitch(enabled = buildFlag)
}
