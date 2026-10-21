package com.us.android.core.auth.di

import com.us.android.core.auth.oauth.GoogleAuthorizationFlow
import com.us.android.core.auth.oauth.GoogleOAuthConfig
import com.us.android.core.auth.oauth.GoogleSignIn
import dagger.Binds
import dagger.BindsOptionalOf
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * Google sign-in wiring.
 *
 * [GoogleOAuthConfig] is OPTIONAL: only an application that applies
 * `us.android.google-signin` provides one (`:app`, from its BuildConfig).
 * The partner apps provide nothing, the `Optional` is empty, and
 * [GoogleAuthorizationFlow] reports itself unavailable — the Login screen
 * then shows no Google button, without any app having to know why.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class OAuthModule {

    @BindsOptionalOf
    abstract fun optionalGoogleOAuthConfig(): GoogleOAuthConfig

    @Binds
    @Singleton
    abstract fun bindGoogleSignIn(impl: GoogleAuthorizationFlow): GoogleSignIn
}
