package com.us.android.core.commerce.di

import com.us.android.core.commerce.network.CommerceApi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Wires the commerce endpoints onto the platform's shared Retrofit client.
 *
 * LB-A3 — this module takes the EXISTING [Retrofit] rather than building one.
 * The shared client carries the token authenticator (refresh on 401), the
 * retry interceptor, the tracing interceptor and the CSRF cookie store. A
 * bespoke client here would silently opt commerce out of all four, and the
 * first symptom would be customers being logged out mid-checkout.
 */
@Module
@InstallIn(SingletonComponent::class)
object CommerceModule {

    @Provides
    @Singleton
    fun provideCommerceApi(retrofit: Retrofit): CommerceApi =
        retrofit.create(CommerceApi::class.java)
}
