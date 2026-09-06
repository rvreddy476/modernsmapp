package com.us.android.feature.post.studio

import com.us.android.core.creator.engine.SourceVault
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.ImageLayer
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.ReelPublishPreview

/**
 * What the profile grid draws while a photo post is going up.
 *
 * The tile wants a picture and a line of text before any of the post exists on
 * the server, so it gets the FIRST page's source photo out of the vault and
 * the caption as typed. The source, not the rendered output: the render has
 * not happened yet when the tile first appears, and the uncropped original is
 * a truthful "this is the post you just made" thumbnail.
 *
 * Derived in one place because two callers need the same answer — the
 * ViewModel, so the tile is on the profile the instant the viewer lands there,
 * and the worker, so a publish that outlives the process puts its tile back.
 *
 * [ReelPublishPreview.creationKey] is the PROJECT ID. It is already the
 * publish's identity everywhere else — the unique work name, the frozen
 * operation's live slot — so the queue keyed by it needs no new id and a
 * restarted worker reports against the same entry.
 */
fun studioPublishPreview(project: AndroidCreatorProject, vault: SourceVault): ReelPublishPreview {
    val firstImage = project.pages.firstOrNull()
        ?.layers
        ?.filterIsInstance<ImageLayer>()
        ?.firstOrNull()
    val cover = firstImage
        ?.let { image -> project.sourceAssets.firstOrNull { it.assetId == image.assetRef } }
        ?.let { vault.resolve(it.vaultPath)?.absolutePath }
    return ReelPublishPreview(
        creationKey = project.projectId,
        coverPath = cover,
        caption = project.postText.value,
        kind = PublishKind.PHOTO,
    )
}
