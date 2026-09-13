package com.us.android.core.food.di

import com.us.android.core.food.network.FoodApi
import com.us.android.core.food.network.KitchenApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Wires the food endpoints onto the platform's shared Retrofit client, which
 * carries the token authenticator, retry, tracing and CSRF. Never a bespoke
 * client — see :core:commerce's CommerceModule for the failure that prevents.
 */
@Module
@InstallIn(SingletonComponent::class)
object FoodModule {

    @Provides
    @Singleton
    fun provideFoodApi(retrofit: Retrofit): FoodApi = retrofit.create(FoodApi::class.java)

    /** The restaurant partner's menu, order and earnings routes (Feast A3). */
    @Provides
    @Singleton
    fun provideKitchenApi(retrofit: Retrofit): KitchenApi = retrofit.create(KitchenApi::class.java)
}
