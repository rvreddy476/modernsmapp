package com.us.android.feature.profile.di

import com.us.android.feature.profile.ui.ContentResolverExportTarget
import com.us.android.feature.profile.ui.ExportTarget
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.android.components.ViewModelComponent

/** Download your data (2026-10-24): where the export is written, a seam the ViewModel tests replace. */
@Module
@InstallIn(ViewModelComponent::class)
abstract class AccountDataModule {
    @Binds
    abstract fun bindExportTarget(target: ContentResolverExportTarget): ExportTarget
}
