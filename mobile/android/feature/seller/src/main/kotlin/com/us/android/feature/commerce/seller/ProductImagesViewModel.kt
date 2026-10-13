package com.us.android.feature.commerce.seller

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.feature.commerce.ui.describe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The gallery a seller is assembling.
 *
 * [notice] is a one-off line — "MStore takes eight images" — rather than a
 * failure: the pick still succeeded for the ones that fit.
 */
data class ProductImagesState(
    val images: List<ProductImageDraft> = emptyList(),
    val loading: Boolean = false,
    val attaching: Boolean = false,
    val notice: String? = null,
    val error: String? = null,
) {
    val cover: String? get() = coverKey(images)
    val uploading: Boolean get() = images.any { !it.ready && it.error == null }
    val canAddMore: Boolean get() = images.size < MAX_PRODUCT_IMAGES

    /** Whether the gallery is in a state that can be sent. */
    val canAttach: Boolean get() = images.isNotEmpty() && !uploading && !attaching
}

/**
 * Product images: pick, upload, order, attach.
 *
 * ## Why the ViewModel owns the draft
 *
 * A rotation must not lose a seller's work, and a rotation does not destroy a
 * ViewModel — so the list lives here rather than in `remember`. The confirmed
 * media ids are ALSO written to [SavedStateHandle], which is what survives
 * process death: an asset that has already been uploaded and confirmed must
 * never be uploaded a second time, because the first copy stays in the store
 * until the server's sweep reclaims it.
 *
 * ## The upload is the same four steps as everything else
 *
 * reserve → push → confirm, through the one [MediaUploader] every feature
 * uses, and then a single attach that sends the whole gallery in order. There
 * is no second upload implementation in commerce, deliberately: the second
 * copy is where the two drift and only one of them gets the next fix.
 */
@HiltViewModel
class ProductImagesViewModel @Inject constructor(
    private val repo: CommerceRepository,
    private val uploader: MediaUploader,
    private val sources: MediaSourceResolver,
    private val savedState: SavedStateHandle,
) : ViewModel() {

    private val _state = MutableStateFlow(ProductImagesState())
    val state: StateFlow<ProductImagesState> = _state.asStateFlow()

    init {
        // Confirmed ids from a previous process: their bytes are already in
        // the store, so they come back as ready images rather than re-uploads.
        val confirmed = savedState.get<ArrayList<String>>(KEY_MEDIA_IDS).orEmpty()
        if (confirmed.isNotEmpty()) {
            _state.value = _state.value.copy(
                images = confirmed.map { ProductImageDraft(mediaId = it) },
            )
        }
    }

    /** Loads a product's existing gallery, for the edit case. */
    fun load(productId: String) {
        if (productId.isBlank()) return
        _state.value = _state.value.copy(loading = true, error = null)
        viewModelScope.launch {
            when (val r = repo.productImages(productId)) {
                is CommerceResult.Failure ->
                    _state.value = _state.value.copy(loading = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    val existing = r.value
                        .sortedBy { it.sortOrder }
                        .map { ProductImageDraft(mediaId = it.mediaId, remoteUrl = it.url) }
                    _state.value = _state.value.copy(loading = false, images = existing)
                    rememberIds(existing)
                }
            }
        }
    }

    /**
     * Takes what the picker returned.
     *
     * The cap is applied HERE, at the pick, and the seller is told when their
     * selection was trimmed — silently dropping the ninth photo is the version
     * of this they would find out about on the product page.
     */
    fun onPicked(uris: List<String>) {
        if (uris.isEmpty()) return
        val current = _state.value.images
        val next = addImages(current, uris)
        val trimmed = wouldExceedCap(current, uris.size)
        _state.value = _state.value.copy(
            images = next,
            notice = if (trimmed) "A product can have $MAX_PRODUCT_IMAGES images." else null,
            error = null,
        )
        next.filter { !it.ready && it.error == null && it.total == 0L }
            .forEach { draft -> viewModelScope.launch { upload(draft.uri) } }
    }

    fun remove(key: String) {
        _state.value = _state.value.copy(images = removeImage(_state.value.images, key))
        rememberIds(_state.value.images)
    }

    /** Moves an image one place earlier or later. The first one is the cover. */
    fun move(key: String, offset: Int) {
        _state.value = _state.value.copy(images = moveImage(_state.value.images, key, offset))
        rememberIds(_state.value.images)
    }

    /** Promotes an image to cover by moving it to the front. */
    fun makeCover(key: String) {
        val index = _state.value.images.indexOfFirst { it.key == key }
        if (index <= 0) return
        move(key, -index)
    }

    fun dismissNotice() {
        _state.value = _state.value.copy(notice = null, error = null)
    }

    /**
     * Sends the gallery.
     *
     * The order of [readyMediaIds] IS the gallery order, cover first. Calls
     * back only on success: a seller told "saved" for a refused attach would
     * leave the screen believing the pictures are live.
     */
    fun attach(productId: String, onAttached: () -> Unit) {
        val ids = readyMediaIds(_state.value.images)
        if (ids.isEmpty() || _state.value.attaching) return

        _state.value = _state.value.copy(attaching = true, error = null)
        viewModelScope.launch {
            when (val r = repo.attachProductImages(productId, ids)) {
                is CommerceResult.Failure ->
                    _state.value = _state.value.copy(attaching = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    _state.value = _state.value.copy(attaching = false)
                    onAttached()
                }
            }
        }
    }

    /**
     * One image, from local URI to a confirmed media id.
     *
     * A failure is recorded ON THE IMAGE rather than on the screen: with eight
     * uploads in flight, one screen-level error line cannot say which photo
     * failed, and the seller's only recourse would be to start again.
     */
    private suspend fun upload(uri: String) {
        val picked = readPicked(uri) ?: return

        edit(uri) { it.copy(total = picked.sizeBytes, uploaded = 0, error = null) }
        val init = when (val r = uploader.reserve(picked.mimeType, picked.sizeBytes)) {
            is AppResult.Success -> r.data
            is AppResult.Failure -> return failImage(uri, "The upload could not start.")
        }

        val put = uploader.upload(
            uploadUrl = init.uploadUrl,
            mimeType = picked.mimeType,
            sizeBytes = picked.sizeBytes,
            source = picked.source,
        ) { sent, total -> edit(uri) { it.copy(uploaded = sent, total = total) } }

        when (put) {
            is PresignedPutResult.Success -> Unit

            // An expired link can never succeed on retry — a new reservation
            // is required — so it is said differently from a transport failure.
            is PresignedPutResult.UrlExpired ->
                return failImage(uri, "The link expired. Add this photo again.")

            is PresignedPutResult.Failed ->
                return failImage(uri, "That photo did not finish uploading.")
        }

        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            return failImage(uri, "That photo could not be confirmed.")
        }

        edit(uri) { it.copy(mediaId = init.mediaId, error = null) }
        rememberIds(_state.value.images)
    }

    /**
     * Reads the picked file, refusing what a buyer could not see.
     *
     * Checked before the upload rather than left to the server, because the
     * round trip is a whole upload: an 18 MB HEIC rejected on attach has cost
     * the seller their upload and told them nothing until it finished.
     */
    private fun readPicked(uri: String): PickedMedia? {
        val picked = sources.resolve(uri)
        val rejection = when {
            picked == null -> "That file could not be read."
            picked.mimeType !in AcceptedImageTypes -> "Product photos must be JPEG, PNG or WebP."
            picked.sizeBytes > MAX_IMAGE_BYTES -> "That photo is larger than 8 MB."
            else -> null
        }
        if (rejection != null) {
            failImage(uri, rejection)
            return null
        }
        return picked
    }

    private fun failImage(uri: String, message: String) = edit(uri) { it.copy(error = message) }

    private fun edit(uri: String, transform: (ProductImageDraft) -> ProductImageDraft) {
        _state.value = _state.value.copy(
            images = _state.value.images.map { if (it.uri == uri) transform(it) else it },
        )
    }

    /** What survives process death: the ids of assets already in the store. */
    private fun rememberIds(images: List<ProductImageDraft>) {
        savedState[KEY_MEDIA_IDS] = ArrayList(readyMediaIds(images))
    }

    private companion object {
        const val KEY_MEDIA_IDS = "productImageMediaIds"
    }
}

/** What a buyer's browser and this app can both display. */
internal val AcceptedImageTypes = setOf("image/jpeg", "image/png", "image/webp")

internal const val MAX_IMAGE_BYTES = 8L * 1024 * 1024
