package com.us.android.feature.premium.di

import com.us.android.feature.premium.data.PremiumApi
import com.us.android.feature.premium.ui.ContentResolverInvoiceTarget
import com.us.android.feature.premium.ui.InvoiceFileTarget
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Creates the Premium endpoints from the app-wide [Retrofit] (the
 * authenticated client). No second stack. The [com.us.android.feature.premium.PremiumPurchaseSwitch]
 * is `:app`'s to provide, from its build flag.
 */
@Module
@InstallIn(SingletonComponent::class)
object PremiumModule {

    @Provides
    @Singleton
    fun providePremiumApi(retrofit: Retrofit): PremiumApi = retrofit.create(PremiumApi::class.java)
}

/** Where a saved invoice is written: the document the buyer picked. */
@Module
@InstallIn(SingletonComponent::class)
abstract class PremiumFilesModule {

    @Binds
    abstract fun bindInvoiceFileTarget(impl: ContentResolverInvoiceTarget): InvoiceFileTarget
}
