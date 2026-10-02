package com.us.android.feature.dating.di

import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.feature.dating.safety.DatingTeardown
import com.us.android.feature.dating.safety.KindAnswerStore
import com.us.android.feature.dating.safety.PrefsKindAnswerStore
import com.us.android.feature.dating.clips.AndroidVideoDurationReader
import com.us.android.feature.dating.clips.AndroidVoiceRecorder
import com.us.android.feature.dating.clips.ClipUploader
import com.us.android.feature.dating.clips.MediaClipUploader
import com.us.android.feature.dating.clips.VideoDurationReader
import com.us.android.feature.dating.clips.VoiceRecorder
import dagger.multibindings.IntoSet
import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.location.FusedCurrentLocationSource
import com.us.android.feature.dating.network.DatingApi
import com.us.android.feature.dating.photos.PhotoUploader
import com.us.android.feature.dating.photos.MediaPhotoUploader
import com.us.android.feature.dating.selfie.MediaSelfieVideoUploader
import com.us.android.feature.dating.selfie.SelfieVideoUploader
import dagger.Binds
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/**
 * Dating's endpoints on the platform's shared Retrofit client (token
 * authenticator, retry, tracing, CSRF) — never a bespoke client — plus the
 * device ports its screens use, bound so the ViewModels test on the JVM.
 */
@Module
@InstallIn(SingletonComponent::class)
abstract class DatingModule {

    @Binds
    abstract fun bindCurrentLocationSource(impl: FusedCurrentLocationSource): CurrentLocationSource

    @Binds
    abstract fun bindPhotoUploader(impl: MediaPhotoUploader): PhotoUploader

    @Binds
    abstract fun bindSelfieVideoUploader(impl: MediaSelfieVideoUploader): SelfieVideoUploader

    /** Mechanic M15: voice and video prompt answers. */
    @Binds
    abstract fun bindClipUploader(impl: MediaClipUploader): ClipUploader

    @Binds
    abstract fun bindVoiceRecorder(impl: AndroidVoiceRecorder): VoiceRecorder

    @Binds
    abstract fun bindVideoDurationReader(impl: AndroidVideoDurationReader): VideoDurationReader

    /** Mechanic M13: "did this bother you?" answered once per message. */
    @Binds
    abstract fun bindKindAnswerStore(impl: PrefsKindAnswerStore): KindAnswerStore

    /** Mechanics M13/M18: what Dating remembers per account goes at sign-out. */
    @Binds
    @IntoSet
    abstract fun bindDatingTeardown(impl: DatingTeardown): SessionTeardownTask

    companion object {
        @Provides
        @Singleton
        fun provideDatingApi(retrofit: Retrofit): DatingApi = retrofit.create(DatingApi::class.java)
    }
}
