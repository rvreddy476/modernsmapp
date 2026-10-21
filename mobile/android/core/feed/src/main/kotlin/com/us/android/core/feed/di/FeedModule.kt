package com.us.android.core.feed.di

import com.us.android.core.feed.data.AudioTracksApi
import com.us.android.core.feed.data.ChannelApi
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.HashtagPostHydrator
import com.us.android.core.feed.data.PostEditApi
import com.us.android.core.feed.data.SoundsApi
import com.us.android.core.feed.data.VideoFeedApi
import com.us.android.core.feed.data.VideoLibraryApi
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.feed.data.VideoLibraryWrites
import com.us.android.core.feed.ui.sound.LibrarySoundPreview
import com.us.android.core.feed.ui.sound.PlayerLibrarySoundPreview
import com.us.android.core.media.sound.SoundPreviewPlayer
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import retrofit2.Retrofit
import javax.inject.Singleton

/** Creates the feed endpoints from the app-wide [Retrofit]. No second stack. */
@Module
@InstallIn(SingletonComponent::class)
object FeedModule {

    @Provides
    @Singleton
    fun provideFeedApi(retrofit: Retrofit): FeedApi = retrofit.create(FeedApi::class.java)

    @Provides
    @Singleton
    fun provideVideoFeedApi(retrofit: Retrofit): VideoFeedApi = retrofit.create(VideoFeedApi::class.java)

    @Provides
    @Singleton
    fun provideChannelApi(retrofit: Retrofit): ChannelApi = retrofit.create(ChannelApi::class.java)

    @Provides
    @Singleton
    fun provideSoundsApi(retrofit: Retrofit): SoundsApi = retrofit.create(SoundsApi::class.java)

    @Provides
    @Singleton
    fun provideVideoLibraryApi(retrofit: Retrofit): VideoLibraryApi = retrofit.create(VideoLibraryApi::class.java)

    /** media-service's alternate audio tracks (2026-10-21): the picker and the creator's dub manager. */
    @Provides
    @Singleton
    fun provideAudioTracksApi(retrofit: Retrofit): AudioTracksApi = retrofit.create(AudioTracksApi::class.java)

    /** The owner's edit of a published post (2026-10-21). */
    @Provides
    @Singleton
    fun providePostEditApi(retrofit: Retrofit): PostEditApi = retrofit.create(PostEditApi::class.java)

    /** The store writes through the repository; tests hand it a fake. */
    @Provides
    fun provideVideoLibraryWrites(impl: VideoLibraryRepository): VideoLibraryWrites = impl

    /** The real hydrator behind the seam; tests bind the identity instead. */
    @Provides
    fun provideFeedItemHydrator(impl: HashtagPostHydrator): FeedItemHydrator = impl

    /**
     * The Library list's one preview player (library sounds, 2026-10-15).
     * Unscoped on purpose: every list gets its own player, released by the
     * ViewModel that holds it.
     */
    @Provides
    fun provideLibrarySoundPreview(player: SoundPreviewPlayer): LibrarySoundPreview = PlayerLibrarySoundPreview(player)
}
