package com.us.android.core.auth.di

import com.us.android.core.auth.AuthApi
import com.us.android.core.auth.KeystoreTokenStore
import com.us.android.core.auth.SessionManager
import com.us.android.core.auth.TokenStore
import com.us.android.core.network.TokenProvider
import com.us.android.core.network.TokenRefresher
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Binds the two interfaces `:core:network` declared but cannot implement.
 *
 * This is the dependency inversion that keeps the module graph acyclic:
 * `:core:auth` depends on `:core:network` for Retrofit, and `:core:network`
 * gets its token access back through these bindings rather than by depending
 * on `:core:auth`.
 */
@Module
@InstallIn(SingletonComponent::class)
object AuthModule {

    @Provides
    @Singleton
    fun provideAuthApi(retrofit: Retrofit): AuthApi = retrofit.create(AuthApi::class.java)

    @Provides
    @Singleton
    fun provideTokenProvider(sessionManager: SessionManager): TokenProvider = sessionManager

    @Provides
    @Singleton
    fun provideTokenRefresher(sessionManager: SessionManager): TokenRefresher = sessionManager
}

@Module
@InstallIn(SingletonComponent::class)
abstract class TokenStoreModule {
    @Binds
    @Singleton
    abstract fun bindTokenStore(impl: KeystoreTokenStore): TokenStore
}
