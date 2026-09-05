package com.us.android.payment

import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * Binds the PSP implementation.
 *
 * One line, and it is the only place in the app that names a provider.
 * Swapping Razorpay for another PSP is a change here plus a new
 * [PaymentLauncher] — not a change to any screen, ViewModel or route, because
 * nothing above this binding knows which provider is wired.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class PaymentModule {

    @Binds
    @Singleton
    abstract fun bindPaymentLauncher(impl: RazorpayPaymentLauncher): PaymentLauncher
}
