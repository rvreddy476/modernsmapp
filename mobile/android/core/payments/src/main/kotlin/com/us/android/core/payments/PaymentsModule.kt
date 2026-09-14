package com.us.android.core.payments

import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

/**
 * Binds the PSP implementation.
 *
 * The only place in the build that names a provider. Swapping Razorpay for
 * another PSP is a change here plus a new [PaymentLauncher] (and a matching
 * [ActivityPaymentHost]) — not a change to any product, screen, ViewModel or
 * route, because nothing above this binding knows which provider is wired.
 *
 * Both bindings resolve to the SAME singleton: the sink the Activity forwards
 * into must be the launcher that holds the in-flight slot.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class PaymentsModule {

    @Binds
    @Singleton
    abstract fun bindPaymentLauncher(impl: RazorpayPaymentLauncher): PaymentLauncher

    @Binds
    @Singleton
    abstract fun bindPaymentResultSink(impl: RazorpayPaymentLauncher): PaymentResultSink
}
